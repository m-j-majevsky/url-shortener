package audit

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// filledEvent возвращает событие, у которого все поля заполнены непустыми
// и попарно различающимися значениями: если какое-то из полей после Reset
// останется прежним, это заметно в любом кейсе.
func filledEvent() AuditEvent {
	return AuditEvent{
		TS:     1730000000,
		Action: ActionShorten,
		UserID: "user-1",
		URL:    "https://example.com/very/long/path",
	}
}

// assertAllZero проверяет через reflection, что все поля события нулевые.
// Перебор по полям вместо перечисления по именам: при добавлении поля
// в AuditEvent и перегенерации reset.gen.go тест менять не придётся,
// но и пропуск поля генератором тест не позволит.
func assertAllZero(t *testing.T, event AuditEvent) {
	t.Helper()

	value := reflect.ValueOf(event)
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		assert.True(t, field.IsZero(),
			"поле %s не нулевое после Reset: %#v", value.Type().Field(i).Name, field.Interface())
	}
}

// TestReset_ClearsFields проверяет базовый контракт сгенерированного Reset:
// сброс заполненного события, идемпотентность повторного вызова и устойчивость
// к вызову на изначально нулевом событии.
func TestReset_ClearsFields(t *testing.T) {
	tests := []struct {
		name  string
		build func() AuditEvent
	}{
		{
			name:  "сброс заполненного события",
			build: filledEvent,
		},
		{
			name: "повторный Reset на уже сброшенном событии ничего не меняет",
			build: func() AuditEvent {
				event := filledEvent()
				event.Reset()
				return event
			},
		},
		{
			name: "Reset на изначально нулевом событии",
			build: func() AuditEvent {
				return AuditEvent{}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := tt.build()

			event.Reset()

			assert.Equal(t, int64(0), event.TS)
			assert.Equal(t, "", event.Action)
			assert.Equal(t, "", event.UserID)
			assert.Equal(t, "", event.URL)
			assertAllZero(t, event)
		})
	}
}

// TestReset_ReusableAfterReset фиксирует контракт переиспользования:
// после Reset тот же экземпляр заполняется новыми значениями, и старые
// значения этому не мешают. Отдельной защиты от регрессий тест не даёт —
// любая реализация, проходящая TestReset_ClearsFields, проходит и этот
// (присваивание полей не зависит от их прежних значений). Ценность теста
// документирующая: Reset в проекте существует ради переиспользования
// объектов, и здесь это намерение закреплено примером использования.
func TestReset_ReusableAfterReset(t *testing.T) {
	event := filledEvent()
	event.Reset()

	// Заполняем тем же экземпляром новыми значениями — как при реальном
	// переиспользовании объекта из пула.
	event.TS = 1740000000
	event.Action = ActionFollow
	event.UserID = "user-2"
	event.URL = "https://another.example.org/path"

	assert.Equal(t, int64(1740000000), event.TS)
	assert.Equal(t, ActionFollow, event.Action)
	assert.Equal(t, "user-2", event.UserID)
	assert.Equal(t, "https://another.example.org/path", event.URL)
}
