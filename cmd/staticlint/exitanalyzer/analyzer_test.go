package exitanalyzer_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/m-j-majevsky/url-shortener/cmd/staticlint/exitanalyzer"
)

func TestExitInMain(t *testing.T) {
	// Сигнатура: Run(t, dir, analyzer, patterns...); dir — каталог testdata,
	// паттерны — импортные пути относительно testdata/src. Каталог testdata
	// не содержит go.mod, поэтому t.Run трактует его как корень GOPATH-дерева.
	analysistest.Run(t, analysistest.TestData(), exitanalyzer.Analyzer, "mainpkg", "other")
}
