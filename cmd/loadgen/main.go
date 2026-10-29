// cmd/loadgen/main.go — генератор нагрузки для исследования производительности.
// Одинаковые -n, -seed и -warmup дают идентичный набор запросов между прогонами,
// что делает корректным сравнение heap-профилей через pprof -diff_base.
//
// Режимы:
//   -mode post   — одиночные POST / с уникальными URL (пиковая нагрузка по памяти);
//   -mode get    — GET /{token} по токенам, собранным на прогреве (307, без сжатия);
//   -mode batch  — POST /api/shorten/batch с JSON-пакетами по -batch элементов.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const letters = "abcdefghijklmnopqrstuvwxyz"

// word возвращает случайное слово из строчных букв заданной длины.
func word(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

// hostURL строит реалистичный URL длиной 60–200 символов.
// TLD-суффикс ("seed" или "bench") разделяет прогревочные и измеряемые URL:
// они гарантированно не пересекаются, и сервис не отдаёт ложные 409.
func hostURL(r *rand.Rand, tld string) string {
	return fmt.Sprintf("https://%s.%s.%s.%s/%s/%s?id=%d",
		word(r, 3+r.Intn(6)), word(r, 4+r.Intn(6)), word(r, 2+r.Intn(3)), tld,
		word(r, 5+r.Intn(10)), word(r, 5+r.Intn(10)), r.Int63())
}

func makeSeedURL(r *rand.Rand) string { return hostURL(r, "seed") }
func makeURL(r *rand.Rand) string     { return hostURL(r, "bench") }

func main() {
	var (
		n     = flag.Int("n", 20000, "число запросов измеряемой фазы")
		c     = flag.Int("c", 50, "одновременных воркеров")
		warm  = flag.Int("warmup", 5000, "сколько URL засеять до измерения")
		mode  = flag.String("mode", "post", "post | get | batch")
		batch = flag.Int("batch", 5, "элементов в пакете (mode=batch)")
		base  = flag.String("base", "http://localhost:8080", "адрес сервиса")
		seed  = flag.Int64("seed", 42, "сид генератора тестовых данных")
	)
	flag.Parse()

	rng := rand.New(rand.NewSource(*seed))

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: *c},
		// Не следовать за 307-редиректами: цель ведёт на внешний URL,
		// и нагрузка ушла бы из локального контура.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	post := func(u string) (string, int, error) {
		resp, err := client.Post(*base+"/", "text/plain", strings.NewReader(u))
		if err != nil {
			return "", 0, err
		}
		defer resp.Body.Close()
		var sb strings.Builder
		_, _ = io.Copy(&sb, resp.Body)
		return sb.String(), resp.StatusCode, nil
	}

	// --- Прогрев: засеять хранилище реалистичным набором данных ---
	tokens := make([]string, 0, *warm)
	for i := 0; i < *warm; i++ {
		body, status, err := post(makeSeedURL(rng))
		if err != nil || status != http.StatusCreated {
			fmt.Printf("прогрев: сбой на запросе %d: err=%v status=%d body=%q\n", i, err, status, body)
			os.Exit(1)
		}
		if tok, ok := strings.CutPrefix(body, *base+"/"); ok {
			tokens = append(tokens, tok)
		}
	}
	fmt.Printf("прогрев: засеяно %d URL\n", len(tokens))
	if *mode == "get" && len(tokens) == 0 {
		fmt.Println("mode=get требует прогрева (warmup > 0)")
		os.Exit(1)
	}

	// --- Измеряемая фаза ---
	var (
		jobs   = make(chan int, *c)
		wg     sync.WaitGroup
		mu     sync.Mutex
		stat   = map[int]int{}
		record = func(code int) {
			mu.Lock()
			stat[code]++
			mu.Unlock()
		}
	)

	start := time.Now()
	for w := 0; w < *c; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			// Детерминизм: сид воркера = seed + 1_000_000 + номер воркера.
			lr := rand.New(rand.NewSource(*seed + 1_000_000 + int64(worker)))
			for range jobs {
				switch *mode {
				case "post":
					_, code, err := post(makeURL(lr))
					if err != nil {
						code = 0
					}
					record(code)
				case "get":
					resp, err := client.Get(*base + "/" + tokens[lr.Intn(len(tokens))])
					if err != nil {
						record(0)
						continue
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					record(resp.StatusCode)
				case "batch":
					items := make([]map[string]string, *batch)
					for j := range items {
						items[j] = map[string]string{
							"correlation_id": fmt.Sprint(lr.Int63()),
							"original_url":   makeURL(lr),
						}
					}
					payload, err := json.Marshal(items)
					if err != nil {
						record(0)
						continue
					}
					resp, err := client.Post(*base+"/api/shorten/batch", "application/json",
						strings.NewReader(string(payload)))
					if err != nil {
						record(0)
						continue
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					record(resp.StatusCode)
				}
			}
		}(w)
	}
	for i := 0; i < *n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)
	fmt.Printf("нагрузка: %d запросов за %s (%.0f rps)\n", *n, elapsed, float64(*n)/elapsed.Seconds())
	codes := make([]int, 0, len(stat))
	for code := range stat {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		fmt.Printf("  HTTP %d: %d\n", code, stat[code])
	}
}
