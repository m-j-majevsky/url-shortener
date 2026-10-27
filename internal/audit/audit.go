// Package audit реализует аудит запросов сервиса на основе паттерна «Наблюдатель».
//
// Роль наблюдателей (приёмников аудита) исполняют реализации интерфейса Observer:
// FileSink (запись в файл) и HTTPSink (отправка POST-запросом на удалённый сервер).
// Роль наблюдаемого объекта («субъекта») исполняет Broadcaster: он собирает
// зарегистрированных наблюдателей и рассылает им события аудита.
//
// Рассылка построена на каналах: события попадают в буферизованный входной канал,
// диспетчер раскладывает их по личным очередям наблюдателей, и у каждого
// наблюдателя работает ровно один фиксированный воркер. Такой дизайн исключает
// размножение горутин под нагрузкой и гонку Notify ↔ Close, свойственную схеме,
// где на каждый Notify создаётся новая горутина под WaitGroup.
//
// Если ни один приёмник не настроен, Broadcaster остаётся без наблюдателей,
// и его Notify фактически превращается в no-op: аудит отключён.
package audit

import (
	"fmt"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"go.uber.org/zap"
)

// Допустимые значения поля Action события аудита.
const (
	ActionShorten = "shorten" // создание короткой ссылки
	ActionFollow  = "follow"  // переход по короткой ссылке
)

// AuditEvent — событие аудита.
type AuditEvent struct {
	Ts     int64  `json:"ts"`
	Action string `json:"action"`
	UserID string `json:"user_id"`
	URL    string `json:"url"`
}

// Observer — «наблюдатель»: приёмник событий аудита.
type Observer interface {
	Send(event AuditEvent) error
}

// Notifier — часть интерфейса «субъекта», необходимая хэндлерам:
// точка диспетчеризации событий аудита. Реализуется Broadcaster'ом.
type Notifier interface {
	Notify(event AuditEvent)
}

// Ёмкости каналов Broadcaster'а подобраны с расчётом на всплески нагрузки:
// входной буфер сглаживает пики запросов, личные очереди сглаживают
// замедление отдельных приёмников.
const (
	eventQueueSize   = 1024 // ёмкость входного канала событий
	observerQueueCap = 64   // ёмкость личной очереди каждого наблюдателя
)

// observerPipe — наблюдатель и его личная очередь событий.
type observerPipe struct {
	observer Observer
	queue    chan AuditEvent
}

// Broadcaster — «субъект» в паттерне «Наблюдатель»: рассылает события
// всем зарегистрированным приёмникам.
//
// Асинхронность обеспечивает фиксированный набор горутин: один диспетчер,
// читающий входной канал, и по одному воркеру на каждого наблюдателя.
// Медлительность одного приёмника не блокирует ни обработку запроса,
// ни доставку другим приёмникам. Ошибки отправки не прерывают доставку,
// а логируются.
type Broadcaster struct {
	events    chan AuditEvent
	pipes     []observerPipe
	wg        sync.WaitGroup
	closeOnce sync.Once

	// mu защищает события и флаг closed: Close закрывает канал под блокировкой,
	// Notify отправляет событие только в незакрытый канал. Благодаря этому Notify
	// безопасно вызывать и во время graceful shutdown — даже когда server.Shutdown
	// завершился по таймауту и хэндлеры в полёте продолжают вызывать Notify.
	mu     sync.RWMutex
	closed bool
}

// NewBroadcaster создаёт субъект аудита с перечисленными наблюдателями
// и запускает фиксированные воркеры: диспетчер и по одному на наблюдателя.
func NewBroadcaster(observers ...Observer) *Broadcaster {
	b := &Broadcaster{
		events: make(chan AuditEvent, eventQueueSize),
	}

	for _, obs := range observers {
		pipe := observerPipe{observer: obs, queue: make(chan AuditEvent, observerQueueCap)}
		b.pipes = append(b.pipes, pipe)

		// Воркер наблюдателя: последовательно отправляет события из его очереди.
		// Завершается, когда диспетчер закроет очередь и та опустеет.
		b.wg.Add(1)
		go func(p observerPipe) {
			defer b.wg.Done()
			for event := range p.queue {
				if err := p.observer.Send(event); err != nil {
					logger.Log.Error("audit event sending failed", zap.Error(err))
				}
			}
		}(pipe)
	}

	// Диспетчер: читает входной канал и раскладывает события по очередям
	// наблюдателей. Когда входной канал закрыт и опустошён, закрывает
	// личные очереди наблюдателей и завершается.
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer func() {
			for _, p := range b.pipes {
				close(p.queue)
			}
		}()

		for event := range b.events {
			for _, p := range b.pipes {
				select {
				case p.queue <- event:
				default:
					// Очередь конкретного наблюдателя переполнена: событие для него
					// отбрасываем, чтобы медленный приёмник не задерживал диспетчер
					// и доставку остальным приёмникам.
					logger.Log.Error("audit observer queue overflow, event dropped",
						zap.String("sink", fmt.Sprintf("%T", p.observer)))
				}
			}
		}
	}()

	return b
}

// Notify асинхронно отправляет событие во все зарегистрированные приёмники.
// Метод никогда не блокирует хэндлер: если входной буфер переполнен, событие
// отбрасывается с записью в лог. Метод безопасно вызывать при отсутствии
// приёмников, на nil-получателе и после Close (в обоих последних случаях — no-op).
func (b *Broadcaster) Notify(event AuditEvent) {
	if b == nil || len(b.pipes) == 0 {
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}

	select {
	case b.events <- event:
	default:
		logger.Log.Error("audit event queue overflow, event dropped",
			zap.String("audit event", fmt.Sprintf("%v", event)))
	}
}

// Close останавливает приём новых событий и дожидается полной доставки
// всех ранее отправленных: диспетчер дораздаёт накопившиеся события наблюдателям,
// воркеры доотправляют их, после чего Close разблокируется.
// Метод идемпотентен.
func (b *Broadcaster) Close() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		close(b.events)
		b.mu.Unlock()
	})
	b.wg.Wait()
}
