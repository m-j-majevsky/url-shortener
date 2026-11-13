// Package exitanalyzer содержит анализатор exitinmain: он запрещает
// прямой вызов os.Exit в функции main пакета main.
//
// Мотивация: прямой выход из main теряет deferred-вызовы (сохранение
// состояния, graceful shutdown), а сам пакет main перестаёт быть пригодным
// для встраивания. Аварийное завершение допустимо через log.Fatal: он
// вызывает os.Exit внутри библиотеки, что прямым вызовом не считается.
//
// Прямым вызовом считается os.Exit, разрешённый через типизацию:
// селектор os.Exit (в том числе с алиасным импортом) и — на случай
// dot-импорта — голый идентификатор Exit, у которого types-объект —
// функция из пакета os. Вызов os.Exit, сохранённый в переменную
// (f := os.Exit; f(1)), прямым не считается. Проверяется только функция
// main без получателя в пакете main; вызовы в замыканиях внутри main
// тоже преследуются. Обычные функции и методы, включая одноимённую
// функцию main в других пакетах, не трогаются.
package exitanalyzer

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Doc — описание анализатора (видно в go doc и выводе -help).
const Doc = `проверяет, что в функции main пакета main нет прямого вызова os.Exit`

// Analyzer — анализатор, встраиваемый в multichecker пакета cmd/staticlint.
var Analyzer = &analysis.Analyzer{
	Name: "exitinmain",
	Doc:  Doc,
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	// Пакеты вида pkg.test — сгенерированные go test main-обёртки
	// тестовых бинарей: их main вызывает os.Exit(m.Run()) по
	// построению. Это инфраструктура go test, а не код проекта.
	if strings.HasSuffix(pass.Pkg.Path(), ".test") {
		return nil, nil
	}
	if pass.Pkg.Name() != "main" {
		return nil, nil
	}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "main" || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isOSExit(pass, call.Fun) {
					pass.Reportf(call.Pos(),
						"прямой вызов os.Exit в функции main пакета main: завершайте main возвратом или через log.Fatal")
				}
				return true
			})
		}
	}
	return nil, nil
}

// isOSExit сообщает, что выражение — прямой вызов os.Exit.
func isOSExit(pass *analysis.Pass, fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		ident, ok := f.X.(*ast.Ident)
		if !ok {
			return false
		}
		pkg, ok := pass.TypesInfo.ObjectOf(ident).(*types.PkgName)
		return ok && pkg.Imported().Path() == "os" && f.Sel.Name == "Exit"
	case *ast.Ident:
		fn, ok := pass.TypesInfo.ObjectOf(f).(*types.Func)
		return ok && fn.Pkg().Path() == "os" && fn.Name() == "Exit"
	}
	return false
}
