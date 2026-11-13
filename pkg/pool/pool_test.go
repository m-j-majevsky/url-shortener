package pool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// testObject — «тяжёлый» объект для проверки пула: обладает состоянием,
// которое способно «грязниться», и счётчиком вызовов Reset.
//
// Счётчик resetCalls намеренно не сбрасывается самим Reset(): по нему тесты
// отличают свежесозданный объект от вернувшегося из пула — у нового счётчик
// всегда 0, у переиспользуемого он растёт с каждым возвратом.
type testObject struct {
	Value      int
	Tag        string
	resetCalls int
}

// Reset сбрасывает состояние объекта, но не счётчик вызовов: см. testObject.
func (o *testObject) Reset() {
	o.Value = 0
	o.Tag = ""
	o.resetCalls++
}

// countingFactory — фабрика со счётчиком созданий: позволяет проверить,
// что пул вызывает её ровно тогда, когда пуст.
type countingFactory struct {
	created int
}

// newObject создаёт объект и наращивает счётчик.
func (f *countingFactory) newObject() *testObject {
	f.created++
	return &testObject{}
}

// PoolSuite — контейнер сценариев пула, общий стиль тестов проекта.
type PoolSuite struct {
	suite.Suite
}

// TestPoolSuite запускает все сценарии пула.
func TestPoolSuite(t *testing.T) {
	suite.Run(t, new(PoolSuite))
}

// TestGet_EmptyPool_CallsFactory проверяет, что первый Get на пустом пуле
// создаёт объект через фабрику — и делает это ровно один раз. Сценарий не
// зависит от режима детектора гонок: пул пуст, фабрика вызывается в любом
// случае.
func (s *PoolSuite) TestGet_EmptyPool_CallsFactory() {
	factory := &countingFactory{}
	p := New(factory.newObject)

	obj := p.Get()

	s.Assert().NotNil(obj)
	s.Assert().Equal(1, factory.created)
	// Свежий объект сброшен фабрикой, Reset() ещё не вызывался.
	s.Assert().Equal(0, obj.resetCalls)
}

// TestGet_ReusesObjectAfterPut проверяет контракт пула: Put вызывает Reset(),
// а следующий Get выдаёт объект в чистом состоянии.
//
// Проверки на фактическое переиспользование (тот же указатель, фабрика
// не дёргалась) возможны только при выключенном детекторе гонок: под -race
// sync.Pool с вероятностью 1/4 теряет объект при Put (см. race_test.go),
// поэтому Get может вернуть свежий объект от фабрики — это штатное поведение
// стандартной библиотеки, а не нарушение контракта пула.
func (s *PoolSuite) TestGet_ReusesObjectAfterPut() {
	factory := &countingFactory{}
	p := New(factory.newObject)

	obj := p.Get()
	obj.Value = 42
	obj.Tag = "грязное значение"

	// Контракт Put: Reset() вызывается до возврата в пул. Проверяется на
	// самом объекте и не зависит от внутренностей sync.Pool.
	p.Put(obj)
	s.Assert().Equal(1, obj.resetCalls, "Put обязан вызвать Reset перед возвратом в пул")

	if raceDetector {
		// Под детектором переиспользование недетерминировано: проверяем
		// только чистоту выданного состояния.
		got := p.Get()
		s.Assert().Equal(0, got.Value)
		s.Assert().Equal("", got.Tag)
		return
	}

	got := p.Get()

	// В одной горутине sync.Pool детерминирован: объект попадает в private
	// слот и выдаётся следующим Get (наш пул всегда зовёт Reset до возврата).
	s.Assert().Equal(0, got.Value)
	s.Assert().Equal("", got.Tag)
	s.Assert().Equal(1, got.resetCalls)
	// Фабрика не дёргается, пока в пуле есть объект.
	s.Assert().Equal(1, factory.created)
	s.Assert().Same(obj, got)
}

// TestGet_SecondGetWithoutPut_CreatesFreshObject проверяет, что объект после
// Get покидает пул: повторный Get без промежуточного Put не возвращает тот же
// объект, а создаёт новый через фабрику. Сценарий не зависит от режима
// детектора гонок: пул пуст на каждом Get, фабрика вызывается в любом случае.
func (s *PoolSuite) TestGet_SecondGetWithoutPut_CreatesFreshObject() {
	factory := &countingFactory{}
	p := New(factory.newObject)

	first := p.Get()
	second := p.Get()

	s.Assert().Equal(2, factory.created)
	s.Assert().NotSame(first, second)
	s.Assert().Equal(0, second.resetCalls)
}

// TestPut_PoolsMultipleObjects проверяет, что пул хранит несколько объектов
// одновременно и выдаёт их без потерь и без посторонних ссылок.
//
// Под детектором гонок часть положенных объектов теряется (см. race_test.go),
// поэтому полный блок проверок выполняется только при выключенном детекторе.
func (s *PoolSuite) TestPut_PoolsMultipleObjects() {
	factory := &countingFactory{}
	p := New(factory.newObject)

	first := p.Get()
	second := p.Get()
	// Различаем объекты по значению: если бы оба вернулись пустыми,
	// путаница в порядке выдачи осталась бы незамеченной.
	first.Value = 1
	second.Value = 2

	p.Put(first)
	p.Put(second)

	// Контракт Put не зависит от внутренностей sync.Pool: оба объекта
	// сброшены перед возвратом.
	s.Assert().Equal(1, first.resetCalls)
	s.Assert().Equal(1, second.resetCalls)

	if raceDetector {
		// Пул мог потерять положенные объекты: проверяем только то,
		// что Get выдаёт валидные объекты в чистом состоянии.
		for i := 0; i < 2; i++ {
			obj := p.Get()
			if !s.Assert().NotNil(obj) {
				return
			}
			s.Assert().Equal(0, obj.Value)
		}
		return
	}

	remaining := map[*testObject]bool{first: true, second: true}
	for i := 0; i < 2; i++ {
		obj := p.Get()
		if s.Assert().True(remaining[obj], "из пула выдан посторонний объект") {
			delete(remaining, obj)
		}
		// Состояние сброшено перед возвратом, независимо от порядка выдачи.
		s.Assert().Equal(0, obj.Value)
	}
	s.Assert().Empty(remaining, "не все положенные в пул объекты выданы")
	// Фабрика не дёргается: обоих объектов хватает на два Get.
	s.Assert().Equal(2, factory.created)
}

// TestPanicCases проверяет fail-fast поведение, обещанное godoc пакета:
// nil-фабрика, nil-результат фабрики и Put(nil) паникуют с точными
// сообщениями — объект-пустышка или nil уехал бы далеко от места ошибки.
func TestPanicCases(t *testing.T) {
	tests := []struct {
		name string
		run  func()
		msg  string
	}{
		{
			name: "nil-фабрика в New",
			run: func() {
				New[*testObject](nil)
			},
			msg: "pool.New: фабрика объектов не может быть nil",
		},
		{
			name: "фабрика вернула nil",
			run: func() {
				p := New(func() *testObject { return nil })
				p.Get()
			},
			msg: "pool.New: фабрика вернула nil — объекты пула не могут быть нулевыми",
		},
		{
			name: "Put(nil)",
			run: func() {
				p := New(func() *testObject { return &testObject{} })
				p.Put(nil)
			},
			msg: "pool.Put: nil нельзя помещать в пул",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.PanicsWithValue(t, tt.msg, tt.run)
		})
	}
}

// TestConcurrentGetPut проверяет потокобезопасность пула: несколько горутин
// одновременно забирают объекты, «грязнят» их и возвращают. Запускать под
// go test -race — детектор гонок подтвердит отсутствие гонок на внутреннем
// состоянии пула. Ассерты здесь не зависят от режима детектора: пул не
// гарантирует выдачу конкретного положенного объекта, а чистоту состояния —
// гарантирует.
func TestConcurrentGetPut(t *testing.T) {
	p := New(func() *testObject { return &testObject{} })

	const goroutines = 8
	const cycles = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < cycles; i++ {
				obj := p.Get()
				if !assert.NotNil(t, obj) {
					// FailNow (require) из не-тестовой горутины запрещён:
					// фиксируем провал через assert и выходим сами.
					return
				}
				obj.Value = i
				obj.Tag = "конкурентное использование"
				p.Put(obj)
			}
		}()
	}
	wg.Wait()

	// Пул жив: объекты продолжают выдаваться и сохраняют контракт сброса.
	obj := p.Get()
	assert.Equal(t, 0, obj.Value)
	assert.Equal(t, "", obj.Tag)
	p.Put(obj)
}
