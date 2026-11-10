package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"honnef.co/go/tools/staticcheck"

	"github.com/kisielk/errcheck/errcheck"
	"github.com/nishanths/exhaustive/passes/exhaustive"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/m-j-majevsky/url-shortener/cmd/staticlint/exitanalyzer"

	// Стандартные анализаторы vet-набора из пакета passes.
	"golang.org/x/tools/go/analysis/passes/appends"
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/cgocall"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/defers"
	"golang.org/x/tools/go/analysis/passes/directive"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/framepointer"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/ifaceassert"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/shadow"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/sigchanyzer"
	"golang.org/x/tools/go/analysis/passes/slog"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stdversion"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/testinggoroutine"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/timeformat"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
)

//go:embed errcheck.exclude
var errcheckExclude []byte

// defaultConfig — имя файла конфигурации, используемого, если флаг
// -config не передан и файл существует в текущем каталоге (make staticlint
// всегда запускается из корня проекта).
const defaultConfig = "config.json"

// defaultErrcheckExclude устанавливает для errcheck зашитый список
// исключений (cmd/staticlint/errcheck.exclude, go:embed). errcheck в
// go/analysis-режиме принимает список только через флаг -errcheck.exclude
// с путём к файлу. Явно переданный флаг перекроет дефолт штатно.
// Временный файл не удаляется:
// multichecker.Main завершает процесс, не возвращая управление.
func defaultErrcheckExclude() error {
	f, err := os.CreateTemp("", "errcheck-exclude-*.txt")
	if err != nil {
		return fmt.Errorf("не удалось создать временный файл исключений errcheck: %w", err)
	}
	if _, err := f.Write(errcheckExclude); err != nil {
		f.Close()
		return fmt.Errorf("не удалось записать временный файл исключений errcheck: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("не удалось записать временный файл исключений errcheck: %w", err)
	}
	if err := errcheck.Analyzer.Flags.Set("exclude", f.Name()); err != nil {
		return fmt.Errorf("не удалось задать исключения errcheck: %w", err)
	}
	return nil
}

// vetAnalyzers — стандартные анализаторы из
// golang.org/x/tools/go/analysis/passes: дефолтный набор go vet
// (33 анализатора) плюс shadow — проверка затенённых переменных,
// в дефолтный vet не входящая. Список зашит явно (без рефлексии по
// пакетам), чтобы состав набора был виден и документирован в одном месте.
var vetAnalyzers = []*analysis.Analyzer{
	appends.Analyzer,
	asmdecl.Analyzer,
	assign.Analyzer,
	atomic.Analyzer,
	bools.Analyzer,
	buildtag.Analyzer,
	cgocall.Analyzer,
	composite.Analyzer,
	copylock.Analyzer,
	defers.Analyzer,
	directive.Analyzer,
	errorsas.Analyzer,
	framepointer.Analyzer,
	httpresponse.Analyzer,
	ifaceassert.Analyzer,
	loopclosure.Analyzer,
	lostcancel.Analyzer,
	nilfunc.Analyzer,
	printf.Analyzer,
	shadow.Analyzer,
	shift.Analyzer,
	sigchanyzer.Analyzer,
	slog.Analyzer,
	stdmethods.Analyzer,
	stdversion.Analyzer,
	stringintconv.Analyzer,
	structtag.Analyzer,
	testinggoroutine.Analyzer,
	tests.Analyzer,
	timeformat.Analyzer,
	unmarshal.Analyzer,
	unreachable.Analyzer,
	unsafeptr.Analyzer,
	unusedresult.Analyzer,
}

// stAnalyzers — класс ST пакета staticcheck, включённый в набор
// (по нумерации staticcheck это диапазон ST1000-ST1005; в нём есть
// пропуски: ST1002 и ST1004 не существуют).
var stAnalyzers = map[string]bool{
	"ST1000": true,
	"ST1001": true,
	"ST1003": true,
	"ST1005": true,
}

// collectAnalyzers собирает полный набор анализаторов: vet-набор,
// все SA и выбранные ST из staticcheck, errcheck, exhaustive и
// собственный exitinmain.
func collectAnalyzers() []*analysis.Analyzer {
	all := make([]*analysis.Analyzer, 0, len(vetAnalyzers)+len(staticcheck.Analyzers)+3)
	all = append(all, vetAnalyzers...)
	for _, sc := range staticcheck.Analyzers {
		name := sc.Analyzer.Name
		if strings.HasPrefix(name, "SA") || stAnalyzers[name] {
			all = append(all, sc.Analyzer)
		}
	}
	all = append(all, errcheck.Analyzer, exhaustive.Analyzer, exitanalyzer.Analyzer)
	return all
}

// lintConfig — формат JSON-файла конфигурации набора (см. godoc).
type lintConfig struct {
	Disabled []string `json:"disabled"`
}

// applyConfig фильтрует набор анализаторов по конфигурации: удаляет
// перечисленные в поле disabled имена и возвращает остаток. Неизвестные
// имена считаются ошибкой конфигурации.
func applyConfig(all []*analysis.Analyzer, path string) ([]*analysis.Analyzer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать конфигурацию: %w", err)
	}
	var cfg lintConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("не удалось разобрать конфигурацию %s: %w", path, err)
	}
	known := make(map[string]bool, len(all))
	for _, a := range all {
		known[a.Name] = true
	}
	disabled := make(map[string]bool, len(cfg.Disabled))
	var unknown []string
	for _, name := range cfg.Disabled {
		if known[name] {
			disabled[name] = true
		} else {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("в конфигурации %s указаны неизвестные анализаторы: %s",
			path, strings.Join(unknown, ", "))
	}
	filtered := make([]*analysis.Analyzer, 0, len(all))
	for _, a := range all {
		if !disabled[a.Name] {
			filtered = append(filtered, a)
		}
	}
	return filtered, nil
}

// configFlag регистрирует флаг -config в общем пространстве флагов:
// имя свободно — checker.RegisterFlags пакета multichecker его не занимает.
// Значение флага читается предсканом os.Args (configPathFromArgs), так как
// настоящий flag.Parse происходит уже внутри multichecker.Main — после
// того, как набор анализаторов собран и отфильтрован. Флаг не задан —
// используется config.json из текущего каталога, если он существует (см. run).
var configFlag = flag.String("config", "",
	"путь к JSON-файлу конфигурации набора анализаторов (см. godoc)")

// configPathFromArgs выковыривает значение -config из os.Args до того,
// как multichecker.Main выполнит flag.Parse. Сканирование останавливается
// на первом аргументе-не-флаге (имени пакета) и на разделителе "--",
// повторяя логику парсера флагов. Краевой случай, когда значение другого
// флага равно строке "-config", считается недопустимым.
func configPathFromArgs(args []string) string {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			break
		}
		if arg == "-config" || arg == "--config" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if v, ok := strings.CutPrefix(arg, "-config="); ok {
			return v
		}
		if v, ok := strings.CutPrefix(arg, "--config="); ok {
			return v
		}
	}
	return ""
}

func run() error {
	if err := defaultErrcheckExclude(); err != nil {
		return err
	}
	all := collectAnalyzers()
	path := configPathFromArgs(os.Args)
	if path == "" {
		if _, err := os.Stat(defaultConfig); err == nil {
			path = defaultConfig
		}
	}
	if path != "" {
		var err error
		if all, err = applyConfig(all, path); err != nil {
			return err
		}
	}
	if len(all) == 0 {
		return errors.New("набор анализаторов пуст")
	}
	multichecker.Main(all...)
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
