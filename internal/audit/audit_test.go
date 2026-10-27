package audit

// Тесты ядра аудита — Broadcaster: рассылка событий наблюдателям, изоляция
// медленных приёмников, переполнение очередей, поведение при Close и
// соответствие формата события требованиям. Сценарии с конкуренцией оформлены
// как тест-сьют; простые сценарии — Table Driven Test'ами.

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

// Compile-time assertion: Broadcaster реализует интерфейс Notifier.
var _ Notifier = (*Broadcaster)(nil)

// chanObserver — тестовый наблюдатель, копирующий полученные события в канал.
//
// Если release != nil, Send блокируется до закрытия release: так моделируется
// «медленный» приёмник, чья отправка зависла в сети. После разблокировки
// события уходят в канал ch; достаточно ёмкости, чтобы после release
// ни один Send больше не блокировался.
type chanObserver struct {
	ch      chan AuditEvent
	release chan struct{}
}

// newFastObserver создаёт быстрого наблюдателя: Send никогда не блокируется,
// события копятся в буферизованном канале.
func newFastObserver(capacity int) *chanObserver {
	return &chanObserver{ch: make(chan AuditEvent, capacity)}
}

// newSlowObserver создаёт «медленного» наблюдателя: первый Send блокируется,
// пока тест не закроет release. Канал ch должен вмещать все доставляемые
// события, иначе Send заблокируется и после разблокировки.
func newSlowObserver(capacity int) *chanObserver {
	return &chanObserver{ch: make(chan AuditEvent, capacity), release: make(chan struct{})}
}

// Send реализует Observer: при блокированном состоянии ждёт разблокировки.
func (o *chanObserver) Send(event AuditEvent) error {
	if o.release != nil {
		<-o.release
	}
	o.ch <- event
	return nil
}

// drain читает все накопившиеся события из канала без блокировки.
func (o *chanObserver) drain() []AuditEvent {
	var events []AuditEvent
	for {
		select {
		case e := <-o.ch:
			events = append(events, e)
		default:
			return events
		}
	}
}

// testEvent — фабрика событий для тестов.
func testEvent(n int) AuditEvent {
	return AuditEvent{
		TS:     1700000000 + int64(n),
		Action: ActionShorten,
		UserID: fmt.Sprintf("user-%d", n),
		URL:    fmt.Sprintf("https://example.com/%d", n),
	}
}

// BroadcasterSuite — сценарии жизненного цикла Broadcaster'а и конкуренции.
type BroadcasterSuite struct {
	suite.Suite
}

// TestBroadcasterSuite запускает тест-сьют Broadcaster'а.
func TestBroadcasterSuite(t *testing.T) {
	suite.Run(t, new(BroadcasterSuite))
}

// TestNotify_NilReceiverAndNoSinks проверяет защиту Notify: вызов на
// nil-получателе и на субъекте без наблюдателей безопасен (no-op),
// без паники и без блокировки. Close для тех же наборов — тоже.
func TestNotify_NilReceiverAndNoSinks(t *testing.T) {
	tests := []struct {
		name string
		b    *Broadcaster
	}{
		{name: "nil broadcaster", b: nil},
		{name: "no observers", b: NewBroadcaster(zap.NewNop())},
		{name: "only nil observers", b: NewBroadcaster(zap.NewNop(), nil, nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				tt.b.Notify(testEvent(1))
			})
			require.NotPanics(t, func() {
				tt.b.Close()
			})
		})
	}
}

// TestBroadcaster_NilObserverSkipped проверяет, что nil-наблюдатель в списке
// конструктора не создаёт воркер и не ломает рассылку остальным наблюдателям.
func TestBroadcaster_NilObserverSkipped(t *testing.T) {
	fast := newFastObserver(16)

	b := NewBroadcaster(zap.NewNop(), nil, fast)
	defer b.Close()

	b.Notify(testEvent(1))
	b.Notify(testEvent(2))

	// Быстрый наблюдатель должен получить оба события: nil-элемент
	// не создаёт «пустой» очереди, но и не влияет на доставку.
	require.Eventually(t, func() bool {
		return len(fast.drain()) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

// TestBroadcaster_DispatchesToAllObservers проверяет базовую рассылку:
// каждое событие доставляется всем зарегистрированным приёмникам
// с полностью сохранёнными полями.
func (s *BroadcasterSuite) TestDispatchesToAllObservers() {
	first := newFastObserver(16)
	second := newFastObserver(16)

	b := NewBroadcaster(zap.NewNop(), first, second)

	want := testEvent(7)
	b.Notify(want)
	b.Close() // дожидаемся доставки всех накопившихся событий

	got1, got2 := first.drain(), second.drain()

	s.Require().Len(got1, 1)
	s.Require().Len(got2, 1)
	s.Assert().Equal(want, got1[0])
	s.Assert().Equal(want, got2[0])
}

// TestBroadcaster_SlowObserverDoesNotBlockOthers проверяет изоляцию личных
// очередей: приёмник, застрявший в Send, не мешает доставке событий другим
// приёмникам и не блокирует Notify.
func (s *BroadcasterSuite) TestSlowObserverDoesNotBlockOthers() {
	slow := newSlowObserver(64)
	fast := newFastObserver(64)

	b := NewBroadcaster(zap.NewNop(), slow, fast)
	defer b.Close()

	const total = 10
	start := time.Now()
	for i := 0; i < total; i++ {
		b.Notify(testEvent(i))
	}
	elapsed := time.Since(start)

	// Notify не должен блокироваться: все вызовы проходят мгновенно.
	s.Require().Less(elapsed, time.Second, "Notify не должен ждать медленных приёмников")

	// Быстрый приёмник получает все события, несмотря на застрявшего соседа.
	s.Require().Eventually(func() bool {
		return len(fast.drain()) == total
	}, 2*time.Second, 10*time.Millisecond)

	// Разблокируем медленного: его воркер доразошлёт накопившиеся события
	// (очередь не переполнена — все total событий уместились).
	close(slow.release)

	s.Assert().Eventually(func() bool {
		return len(slow.drain()) == total
	}, 2*time.Second, 10*time.Millisecond)
}

// TestBroadcaster_QueueOverflowDropsEvent проверяет политику переполнения:
// когда личная очередь медленного наблюдателя заполнена, диспетчер отбрасывает
// для него события, а Notify остаётся неблокирующим.
func (s *BroadcasterSuite) TestQueueOverflowDropsEvent() {
	stuck := newSlowObserver(128) // застрянет в Send до разблокировки

	b := NewBroadcaster(zap.NewNop(), stuck)

	// Отправляем больше событий, чем вмещает личная очередь наблюдателя:
	// часть обязана быть отброшена, пока воркер застрял в Send.
	const total = 200
	start := time.Now()
	for i := 0; i < total; i++ {
		b.Notify(testEvent(i))
	}
	elapsed := time.Since(start)

	// Notify не блокируется даже при переполненных очередях.
	s.Require().Less(elapsed, time.Second)

	// Доставлено может быть не более 1 (застрявшего в Send) + observerQueueCap
	// (в очереди) событий; всё сверх — отброшено. Точная цифра зависит от
	// планировщика, поэтому проверяем границы, а не равенство.
	close(stuck.release)
	b.Close() // дожидаемся, пока воркер разблокированного наблюдателя дочитает очередь

	delivered := len(stuck.drain())
	s.Assert().Less(delivered, total, "часть событий должна быть отброшена")
	s.Assert().LessOrEqual(delivered, 1+observerQueueCap,
		"доставлено не больше, чем вмещают очередь и воркер")
	s.Assert().GreaterOrEqual(delivered, observerQueueCap-1,
		"существенная часть событий должна быть доставлена")

	// Счётчик потерь согласован с фактом: аудит — best-effort, и каждое
	// недоставленное событие должно быть учтено (потери здесь возможны только
	// в диспетчере — входной буфер вмещает все 200 отправленных событий).
	s.Assert().Equal(int64(total-delivered), b.Dropped(),
		"Dropped должен учитывать каждое недоставленное событие")
}

// TestBroadcaster_NotifyAfterClose проверяет, что Notify после Close —
// безопасный no-op: событие не доставляется, паники нет.
func (s *BroadcasterSuite) TestNotifyAfterClose() {
	observer := newFastObserver(16)

	b := NewBroadcaster(zap.NewNop(), observer)
	b.Close()

	require.NotPanics(s.T(), func() {
		b.Notify(testEvent(1))
	})

	// Дадим воркеру шанс (если бы событие попало в очередь) и убедимся,
	// что наблюдатель ничего не получил.
	time.Sleep(100 * time.Millisecond)
	s.Assert().Empty(observer.drain(), "после Close новые события не доставляются")

	// Отброшенное после Close событие не теряется «молча»: оно учтено счётчиком.
	s.Assert().Equal(int64(1), b.Dropped(),
		"событие, отброшенное после Close, должно учитываться в Dropped")
}

// TestBroadcaster_CloseTwiceAndWaitConcurrently проверяет идемпотентность
// Close и одновременный вызов Close из нескольких горутин: без паники,
// с полной доставкой ранее отправленных событий.
func (s *BroadcasterSuite) TestCloseTwiceAndWaitConcurrently() {
	first := newFastObserver(64)
	second := newFastObserver(64)

	b := NewBroadcaster(zap.NewNop(), first, second)

	const total = 20
	for i := 0; i < total; i++ {
		b.Notify(testEvent(i))
	}

	// Одновременный Close из двух горутин — оба вызова легальны.
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			b.Close()
		}()
	}
	wg.Wait()
	// Повторный Close тоже безопасен (идемпотентность).
	b.Close()

	s.Assert().Len(first.drain(), total)
	s.Assert().Len(second.drain(), total)
}

// TestBroadcaster_EmptyObservers проверяет субъект без наблюдателей:
// Close завершается мгновенно (воркеров нет), Notify — no-op.
func (s *BroadcasterSuite) TestEmptyObservers() {
	b := NewBroadcaster(zap.NewNop()) // наблюдателей нет: аудит «выключен»

	start := time.Now()
	b.Notify(testEvent(1))
	b.Close()
	elapsed := time.Since(start)

	s.Assert().Less(elapsed, 100*time.Millisecond,
		"Close на пустом субъекте должен завершаться мгновенно")
}

// TestBroadcaster_CloseDrainsPendingEvents проверяет доставку событий,
// отправленных до Close: диспетчер дораздаёт их из закрывающегося входного
// канала, воркеры доотправляют. Гарантия доставки ограничена ёмкостью
// конвейера, поэтому объём ровно observerQueueCap: при нём переполнение
// личной очереди невозможно при любом планировании — в очереди не бывает
// больше observerQueueCap-1 событий, пока очередное удерживает диспетчер,
// значит проверка детерминирована. Потеря при переполнении — отдельный тест
// (см. TestQueueOverflowDropsEvent).
func (s *BroadcasterSuite) TestCloseDrainsPendingEvents() {
	observer := newFastObserver(observerQueueCap)

	b := NewBroadcaster(zap.NewNop(), observer)

	for i := 0; i < observerQueueCap; i++ {
		b.Notify(testEvent(i))
	}

	// Close должен дождаться доставки всех накопившихся событий.
	b.Close()

	delivered := observer.drain()
	s.Require().Len(delivered, observerQueueCap,
		"все события, отправленные до Close, должны быть доставлены")

	// Поля событий не искажаются, порядок сохраняется (FIFO через конвейер).
	for i, got := range delivered {
		s.Assert().Equal(testEvent(i), got)
	}

	// При таком объёме потерь быть не должно: переполнение исключено по
	// построению, счётчик это подтверждает.
	s.Assert().Zero(b.Dropped(), "при observerQueueCap событиях переполнение невозможно")
}

// TestAuditEvent_JSONTags проверяет соответствие формата события аудита
// требованиям: имена полей ts, action, user_id, url; ts — unix-время в секундах.
func TestAuditEvent_JSONTags(t *testing.T) {
	event := AuditEvent{TS: 12345678, Action: ActionShorten, UserID: "12315134", URL: "https://example.com/path"}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	// Ровно требуемые ключи и никаких других.
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"ts", "action", "user_id", "url"}, keys)

	// Значения соответствуют ожиданиям.
	assert.JSONEq(t, "12345678", string(raw["ts"]))
	assert.JSONEq(t, `"shorten"`, string(raw["action"]))
	assert.JSONEq(t, `"12315134"`, string(raw["user_id"]))
	assert.JSONEq(t, `"https://example.com/path"`, string(raw["url"]))

	// ts — числовой тип (unix-время в секундах), а не строка.
	var typed struct {
		TS int64 `json:"ts"`
	}
	require.NoError(t, json.Unmarshal(data, &typed))
	assert.Equal(t, int64(12345678), typed.TS)
}
