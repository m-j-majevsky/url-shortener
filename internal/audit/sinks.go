package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	// ContentTypeJSON — Content-Type для отправки событий на удалённый приёмник.
	// Захардкожен, чтобы не тянуть зависимость от пакета handler.
	ContentTypeJSON = "application/json"

	// sendTimeout — таймаут одной отправки на удалённый приёмник.
	sendTimeout = 5 * time.Second
)

// FileSink — приёмник аудита, дописывающий события в конец файла
// (каждое событие — отдельная строка с JSON-объектом).
type FileSink struct {
	mu   sync.Mutex // защита от конкурентных дописываний в файл
	file *os.File
}

// NewFileSink открывает (или создаёт) файл-приёмник в режиме дозаписи.
func NewFileSink(path string) (*FileSink, error) {
	// O_APPEND гарантирует атомарность дописывания в конец файла,
	// O_CREATE — создание файла при первом запуске.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open audit file sink %q: %w", path, err)
	}
	return &FileSink{file: file}, nil
}

// Send сериализует событие и дописывает его в конец файла на новой строке.
func (fs *FileSink) Send(event AuditEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("cannot marshal audit event: %w", err)
	}
	data = append(data, '\n')

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, err := fs.file.Write(data); err != nil {
		return fmt.Errorf("cannot write audit event to file: %w", err)
	}
	return nil
}

// Close закрывает файл-приёмник. Вызывается при завершении работы сервиса.
func (fs *FileSink) Close() error {
	return fs.file.Close()
}

// HTTPSink — приёмник аудита, отправляющий события POST-запросом
// на удалённый сервер по URL из конфигурации.
type HTTPSink struct {
	url    string
	client *http.Client
}

// NewHTTPSink создаёт приёмник с ограничением по времени одной отправки.
func NewHTTPSink(url string) *HTTPSink {
	return &HTTPSink{
		url:    url,
		client: &http.Client{Timeout: sendTimeout},
	}
}

// Send отправляет событие методом POST на удалённый приёмник.
func (hs *HTTPSink) Send(event AuditEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("cannot marshal audit event: %w", err)
	}

	resp, err := hs.client.Post(hs.url, ContentTypeJSON, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cannot send audit event to %q: %w", hs.url, err)
	}
	defer resp.Body.Close()

	// Дочитываем и отбрасываем тело ответа, чтобы соединение можно было переиспользовать
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("cannot drain audit sink response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("audit sink %q returned status %d", hs.url, resp.StatusCode)
	}
	return nil
}
