package audit

// Тесты приёмников аудита: FileSink (дозапись событий в файл) и HTTPSink
// (отправка событий POST-запросом на удалённый сервер). Для простых сценариев
// используется подход Table Driven Test, для конкурентных — отдельные тесты.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time assertions: приёмники обязаны удовлетворять интерфейсу Observer.
var (
	_ Observer = (*FileSink)(nil)
	_ Observer = (*HTTPSink)(nil)
)

// TestNewFileSink проверяет конструктор FileSink на корректных и некорректных
// путях: создание нового файла, дозапись в существующий и ошибки доступа.
func TestNewFileSink(t *testing.T) {
	t.Run("creates and appends to existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.jsonl")

		// Первый приёмник создаёт файл и пишет одно событие.
		sink1, err := NewFileSink(path)
		require.NoError(t, err)
		require.NoError(t, sink1.Send(AuditEvent{TS: 1, Action: ActionShorten}))
		require.NoError(t, sink1.Close())

		// Второй приёмник открывает тот же файл: дозапись, а не перезапись.
		sink2, err := NewFileSink(path)
		require.NoError(t, err)
		defer sink2.Close()
		require.NoError(t, sink2.Send(AuditEvent{TS: 2, Action: ActionFollow}))

		data, err := os.ReadFile(path)
		require.NoError(t, err)

		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		require.Len(t, lines, 2, "ожидается дозапись: оба события в файле")

		var first, second AuditEvent
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
		require.NoError(t, json.Unmarshal([]byte(lines[1]), &second))
		assert.Equal(t, int64(1), first.TS)
		assert.Equal(t, int64(2), second.TS)
	})

	t.Run("nonexistent directory returns error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no_such_dir", "audit.jsonl")

		_, err := NewFileSink(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no_such_dir")
	})

	t.Run("path is a directory returns error", func(t *testing.T) {
		dir := t.TempDir()

		_, err := NewFileSink(dir)
		require.Error(t, err)
	})
}

// TestFileSink_Send_WritesOneLine проверяет формат записи: каждое событие —
// валидный JSON на отдельной строке с полями в требуемом порядке
// (ts, action, user_id, url). Таблица покрывает одиночное событие,
// несколько событий подряд и русскоязычную не-ASCII строку.
func TestFileSink_Send_WritesOneLine(t *testing.T) {
	tests := []struct {
		name     string
		events   []AuditEvent
		wantTS   []int64
		wantAct  []string
		wantUser []string
		wantURL  []string
	}{
		{
			name:     "single event",
			events:   []AuditEvent{{TS: 12345678, Action: ActionShorten, UserID: "12315134", URL: "https://mylongdomain.com/my/long/path/to/shorten/"}},
			wantTS:   []int64{12345678},
			wantAct:  []string{ActionShorten},
			wantUser: []string{"12315134"},
			wantURL:  []string{"https://mylongdomain.com/my/long/path/to/shorten/"},
		},
		{
			name: "several events in row",
			events: []AuditEvent{
				{TS: 1, Action: ActionShorten, UserID: "u1", URL: "https://example.com/a"},
				{TS: 2, Action: ActionFollow, UserID: "u2", URL: "https://example.com/b"},
				{TS: 3, Action: ActionShorten, UserID: "", URL: "https://example.com/c"},
			},
			wantTS:   []int64{1, 2, 3},
			wantAct:  []string{ActionShorten, ActionFollow, ActionShorten},
			wantUser: []string{"u1", "u2", ""},
			wantURL:  []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"},
		},
		{
			name:     "non-ascii url",
			events:   []AuditEvent{{TS: 42, Action: ActionFollow, UserID: "u", URL: "https://example.com/привет"}},
			wantTS:   []int64{42},
			wantAct:  []string{ActionFollow},
			wantUser: []string{"u"},
			wantURL:  []string{"https://example.com/привет"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.jsonl")

			sink, err := NewFileSink(path)
			require.NoError(t, err)
			defer sink.Close()

			for _, event := range tt.events {
				require.NoError(t, sink.Send(event))
			}

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			raw := strings.TrimRight(string(data), "\n")
			lines := strings.Split(raw, "\n")
			require.Len(t, lines, len(tt.events), "каждое событие — ровно одна строка")

			for i, line := range lines {
				// Порядок полей в JSON должен следовать объявлению структуры:
				// encoding/json сериализует поля в порядке объявления.
				assert.True(t, strings.HasPrefix(line, `{"ts":`),
					"поле ts должно быть первым в JSON: %q", line)
				assert.NotContains(t, line, "\n", "внутри строки не должно быть переносов")

				var got AuditEvent
				require.NoError(t, json.Unmarshal([]byte(line), &got),
					"строка %d должна быть валидным JSON", i)
				assert.Equal(t, tt.wantTS[i], got.TS)
				assert.Equal(t, tt.wantAct[i], got.Action)
				assert.Equal(t, tt.wantUser[i], got.UserID)
				assert.Equal(t, tt.wantURL[i], got.URL)
			}
		})
	}
}

// TestFileSink_Send_Concurrent проверяет целостность записи при конкурентных
// дописываниях: мьютекс FileSink должен гарантировать, что события не
// «перемешаются» и каждое останется отдельной валидной строкой.
func TestFileSink_Send_Concurrent(t *testing.T) {
	const goroutines = 50
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	sink, err := NewFileSink(path)
	require.NoError(t, err)
	defer sink.Close()

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			event := AuditEvent{
				TS:     int64(n),
				Action: ActionShorten,
				UserID: fmt.Sprintf("user-%d", n),
				URL:    fmt.Sprintf("https://example.com/%d", n),
			}
			assert.NoError(t, sink.Send(event))
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, goroutines, "все события должны быть записаны построчно без склейки")

	// Порядок строк при конкурентной записи не гарантирован, поэтому сверяем
	// события как множество с кратностями: каждое отправленное должно быть
	// записано ровно один раз и без искажений полей.
	got := make(map[AuditEvent]int, goroutines)
	for _, line := range lines {
		var ev AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &ev),
			"каждая строка должна быть целым JSON-объектом: %q", line)
		got[ev]++
	}

	for i := 0; i < goroutines; i++ {
		want := AuditEvent{
			TS:     int64(i),
			Action: ActionShorten,
			UserID: fmt.Sprintf("user-%d", i),
			URL:    fmt.Sprintf("https://example.com/%d", i),
		}
		assert.Equal(t, 1, got[want], "событие %d должно быть записано ровно один раз и без искажений", i)
	}
	assert.Len(t, got, goroutines, "в файле не должно быть лишних или дублирующихся событий")
}

// TestHTTPSink_Send проверяет, что HTTPSink отправляет события POST-запросом
// с правильным Content-Type и телом, а коды ответа сервера интерпретируются
// корректно: 2xx — успех, начиная с 4xx — ошибка. Таблица — по статусам.
func TestHTTPSink_Send(t *testing.T) {
	event := AuditEvent{TS: 12345678, Action: ActionShorten, UserID: "12315134", URL: "https://example.com/path"}

	tests := []struct {
		name       string
		status     int
		wantErr    bool
		wantStatus string // фрагмент текста ошибки, если ожидается
	}{
		{name: "200 ok", status: http.StatusOK},
		{name: "201 created", status: http.StatusCreated},
		{name: "204 no content", status: http.StatusNoContent},
		{name: "400 bad request is error", status: http.StatusBadRequest, wantErr: true, wantStatus: "400"},
		{name: "500 internal is error", status: http.StatusInternalServerError, wantErr: true, wantStatus: "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				gotMethod      string
				gotContentType string
				gotBody        AuditEvent
			)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotContentType = r.Header.Get("Content-Type")

				var decoded AuditEvent
				if err := json.NewDecoder(r.Body).Decode(&decoded); err == nil {
					gotBody = decoded
				}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			sink := NewHTTPSink(server.URL)
			err := sink.Send(event)

			assert.Equal(t, http.MethodPost, gotMethod, "событие отправляется методом POST")
			assert.Equal(t, ContentTypeJSON, gotContentType)
			assert.Equal(t, event, gotBody, "тело запроса должно точно соответствовать событию")

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantStatus)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestHTTPSink_Send_NetworkError проверяет обработку сетевых сбоев:
// недоступный приёмник должен давать ошибку, а не панику или зависание.
func TestHTTPSink_Send_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	// Закрываем сервер сразу: соединения с ним завершатся сетевой ошибкой.
	server.Close()

	sink := NewHTTPSink(server.URL)
	err := sink.Send(AuditEvent{TS: 1, Action: ActionShorten})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "не удалось отправить событие аудита")
}

// TestHTTPSink_Send_Timeout проверяет, что медленный приёмник не блокирует
// отправку дольше sendTimeout: HTTPSink должен вернуть ошибку таймаута.
// Тест занимает реальную sendTimeout (~5 секунд).
func TestHTTPSink_Send_Timeout(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done // держим соединение открытым до конца теста
	}))
	defer server.Close()
	defer close(done)

	sink := NewHTTPSink(server.URL)

	start := time.Now()
	err := sink.Send(AuditEvent{TS: 1, Action: ActionShorten})
	elapsed := time.Since(start)

	require.Error(t, err, "зависший приёмник должен привести к ошибке таймаута")
	assert.Less(t, elapsed, sendTimeout+2*time.Second,
		"отправка не должна блокироваться заметно дольше sendTimeout")
}
