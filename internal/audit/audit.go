// Package audit реализует аудит запросов сервиса на основе паттерна «Наблюдатель».
//
// Роль наблюдателей (приёмников аудита) исполняют реализации интерфейса Observer:
// FileSink (запись в файл) и HTTPSink (отправка POST-запросом на удалённый сервер).
// Роль наблюдаемого объекта («субъекта») исполняет Broadcaster: он собирает
// зарегистрированных наблюдателей и рассылает им события аудита.
//
// Если ни один приёмник не настроен, Broadcaster остаётся без наблюдателей,
// и его Notify фактически превращается в no-op: аудит отключён.
package audit

import (
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

// Broadcaster — «субъект» в паттерне «Наблюдатель»: рассылает события
// всем зарегистрированным приёмникам.
//
// Рассылка выполняется асинхронно:
// каждый приёмник получает событие в отдельной горутине, поэтому сбой
// или медлительность одного приёмника не блокирует обработку запроса.
// Ошибки отправки не прерывают доставку остальным приёмникам, а логируются.
type Broadcaster struct {
	observers []Observer
	wg        sync.WaitGroup
}

// NewBroadcaster создаёт субъект аудита с перечисленными наблюдателями.
func NewBroadcaster(observers ...Observer) *Broadcaster {
	return &Broadcaster{
		observers: append([]Observer(nil), observers...),
	}
}

// Notify асинхронно отправляет событие во все зарегистрированные приёмники.
// Метод безопасно вызывать при отсутствии приёмников и на nil-получателе:
// в обоих случаях это no-op.
func (b *Broadcaster) Notify(event AuditEvent) {
	if b == nil || len(b.observers) == 0 {
		return
	}

	for _, obs := range b.observers {
		b.wg.Add(1)
		go func(o Observer) {
			defer b.wg.Done()
			if err := o.Send(event); err != nil {
				logger.Log.Error("audit event sending failed", zap.Error(err))
			}
		}(obs)
	}
}

// Close блокируется до завершения доставки всех ранее отправленных событий.
// Вызывается при graceful shutdown, чтобы события, отправленные перед остановкой
// сервера, успели дойти до приёмников.
func (b *Broadcaster) Close() {
	if b == nil {
		return
	}
	b.wg.Wait()
}
