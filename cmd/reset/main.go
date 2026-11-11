// Утилита reset генерирует методы Reset() для структур проекта.
//
// Генератор сканирует пакеты, начиная с корневой директории и ниже, находит
// структуры, помеченные комментарием-маркером `// generate:reset;`, и создаёт
// для каждой из них метод Reset(), сбрасывающий состояние объекта к начальным
// значениям. Все сгенерированные методы одного пакета размещаются в файле
// `reset.gen.go` этого пакета.
//
// Правила генерации метода Reset():
//
//   - примитивы и именованные типы над примитивами приводятся к нулевым
//     значениям (int -> 0, string -> "", bool -> false и так далее);
//   - слайсы (в том числе именованные типы над ними) обрезаются по длине
//     без зануления (strings []string -> strings[:0]), ёмкость сохраняется;
//   - мапы очищаются встроенной функцией clear;
//   - массивы сбрасываются присваиванием нулевого значения целиком;
//   - вложенные именованные типы с методом Reset() без параметров и
//     результатов (в том числе указатели на них и типы из чужих пакетов)
//     вызывают этот метод;
//   - структуры и указатели на структуры своего пакета без метода Reset()
//     сбрасываются рекурсивным обходом полей;
//   - экспортируемые структуры без метода Reset() из чужих пакетов
//     сбрасываются присваиванием нулевого значения целиком (zero-assign);
//     неэкспортируемые типы чужих пакетов пропускаются с комментарием;
//   - поля с типами интерфейсов, каналов и функций пропускаются
//     с комментарием — осмысленного сброса для них не существует; исключение:
//     интерфейс с методом Reset() без параметров и результатов вызывает его.
//
// Запуск из корня проекта:
//
//	go run ./cmd/reset .
//
// Единственный позиционный аргумент — корневая директория сканирования
// (пустой аргумент эквивалентен текущей директории).
//
// Файлы reset.gen.go содержат шапку "// Code generated ... DO NOT EDIT."
// и управляются исключительно генератором: ручные правки в них перезаписываются
// при следующем прогоне. Если маркер `// generate:reset;` снять со структуры,
// соответствующий метод будет удалён из reset.gen.go при следующем прогоне.
//
// Утилита игнорирует тестовые файлы (*_test.go), собственные файлы генерации
// (*.gen.go) и каталоги mocks/ — они не считаются боевым кодом проекта.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// marker — точное содержимое строки-маркера после `//`: только комментарий
// с этим текстом включает генерацию метода Reset().
const marker = "generate:reset;"

// genFileName — имя файла со сгенерированными методами пакета.
const genFileName = "reset.gen.go"

// config — параметры прогона генератора.
type config struct {
	root string // корневая директория сканирования
}

// markedStruct — тип проекта, помеченный маркером `// generate:reset;`.
type markedStruct struct {
	pkg  *packages.Package // пакет, в котором объявлен тип
	spec *ast.TypeSpec     // узел объявления типа
	pos  token.Position    // позиция объявления — для сообщений генератора
}

// hasMarker сообщает, содержит ли группа комментариев строку-маркер.
// Сравнение точное: текст однострочного комментария, усечённый по пробелам,
// должен совпадать с marker целиком.
func hasMarker(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		text, ok := strings.CutPrefix(c.Text, "//")
		if !ok {
			continue // блочные комментарии /* ... */ маркером не считаются
		}
		if strings.TrimSpace(text) == marker {
			return true
		}
	}
	return false
}

// skipFile сообщает, что файл не считается боевым кодом и из сканирования
// исключается: сгенерированные файлы (*.gen.go), тестовые файлы и файлы
// из каталогов mocks/ на любом уровне вложенности. Каталоги testdata и _*
// go игнорирует сам при загрузке "./...", отдельной обработки не требуют.
func skipFile(filename string) bool {
	base := filepath.Base(filename)
	if strings.HasSuffix(base, ".gen.go") || strings.HasSuffix(base, "_test.go") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(filename)), "/") {
		if part == "mocks" {
			return true
		}
	}
	return false
}

// collectStructs находит в пакете все типы-структуры с маркером.
//
// Нюанс go/ast: при одиночной декларации вида
//
//	// generate:reset;
//	type X struct{ ... }
//
// go/parser цепляет комментарий к Doc всей декларации (GenDecl.Doc), а не к
// Doc объявления типа (TypeSpec.Doc), поэтому проверяются оба места. Маркер
// в Doc групповой декларации `type ( ... )` применяется только при
// единственном spec в группе — иначе он неявно относился бы ко всем типам
// группы, у которых собственного маркера нет.
//
// Если маркер стоит на типе, не являющемся структурой, генератор печатает
// предупреждение в stderr и тип пропускает.
func collectStructs(pkg *packages.Package) []*markedStruct {
	var found []*markedStruct
	for _, file := range pkg.Syntax {
		if skipFile(pkg.Fset.Position(file.Pos()).Filename) {
			continue
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				if !hasMarker(ts.Doc) && !(len(gd.Specs) == 1 && hasMarker(gd.Doc)) {
					continue
				}
				if _, isStruct := ts.Type.(*ast.StructType); !isStruct {
					fmt.Fprintf(os.Stderr,
						"предупреждение: маркер // %s стоит на не-структуре %s (%s), пропуск\n",
						marker, ts.Name.Name, pkg.Fset.Position(ts.Pos()))
					continue
				}
				found = append(found, &markedStruct{
					pkg:  pkg,
					spec: ts,
					pos:  pkg.Fset.Position(ts.Pos()),
				})
			}
		}
	}
	return found
}

// nodeKind — вид действия над полем в плане метода Reset().
type nodeKind int

const (
	nodeZero  nodeKind = iota // присваивание нулевого значения
	nodeSlice                 // обрезка слайса по длине: path[:0]
	nodeMap                   // очистка мапы: clear(path)
	nodeCall                  // вызов метода Reset()
	nodeIf                    // условный блок: if path != nil { ... }
	nodeSkip                  // пропуск поля с комментарием
)

// node — элемент плана метода: оператор, условный блок или пропуск поля.
type node struct {
	kind nodeKind
	path string // выражение-путь к значению, например "ae.Time" или "*ae.strP"
	zero string // нулевое значение для nodeZero: 0, "", false, pkg.T{}
	cond string // условие для nodeIf
	body []node // вложенные операторы для nodeIf
	note string // пояснение причины пропуска (nodeSkip)
}

// fieldEntry — план сброса одного поля структуры: операторы для поля в
// порядке их появления в методе.
type fieldEntry struct {
	field *types.Var // объявление поля
	root  []node     // операторы плана для поля
}

// structPlan — план метода Reset() для одной помеченной структуры.
type structPlan struct {
	pkg    *packages.Package // пакет структуры
	spec   *ast.TypeSpec     // узел объявления типа
	pos    token.Position    // позиция объявления — для сообщений генератора
	recv   string            // имя приёмника метода
	fields []*fieldEntry     // план по полям в порядке объявления
}

// classifyCtx — контекст классификации полей одного пакета.
type classifyCtx struct {
	pkg      *packages.Package     // пакет, для которого генерируется reset.gen.go
	marked   map[*types.Named]bool // помеченные типы всего проекта
	registry *importRegistry       // реестр импортов сгенерированного файла
	stack    map[*types.Named]bool // типы в текущей ветке рекурсии — защита от циклов
}

// importRegistry — реестр импортов одного сгенерированного файла: имена,
// под которыми чужие пакеты упоминаются в коде. Реестр общий на пакет
// (reset.gen.go один на пакет), поэтому коллизии имён разрешаются
// единообразно для всех структур пакета.
type importRegistry struct {
	pkgPath string            // путь пакета-владельца: для него квалификатор пуст
	names   map[string]string // путь чужого пакета -> имя в коде
	used    map[string]bool   // занятые идентификаторы пакета
	order   []string          // пути чужих пакетов в порядке первого упоминания
}

// newImportRegistry создаёт реестр импортов для пакета pkgPath.
func newImportRegistry(pkgPath string) *importRegistry {
	return &importRegistry{
		pkgPath: pkgPath,
		names:   make(map[string]string),
		used:    make(map[string]bool),
	}
}

// nameFor возвращает имя для обращения к чужому пакету: имя пакета или
// алиас с числовым суффиксом при коллизии (например, ip2 для второго
// пакета с именем ip). Реестр живёт ровно столько, сколько длится
// генерация файла, поэтому резервирование имён не откатывается.
func (r *importRegistry) nameFor(pkg *types.Package) string {
	if name, ok := r.names[pkg.Path()]; ok {
		return name
	}
	candidate := pkg.Name()
	for n := 2; r.used[candidate]; n++ {
		candidate = pkg.Name() + strconv.Itoa(n)
	}
	r.names[pkg.Path()] = candidate
	r.used[candidate] = true
	r.order = append(r.order, pkg.Path())
	return candidate
}

// qualifier возвращает функцию квалификации для types.TypeString: свой
// пакет даёт пустой квалификатор, чужие — имя или алиас из реестра.
func (r *importRegistry) qualifier() types.Qualifier {
	return func(p *types.Package) string {
		if p == nil || p.Path() == r.pkgPath {
			return ""
		}
		return r.nameFor(p)
	}
}

// imports возвращает перечисление импортов для шапки сгенерированного файла:
// без алиаса, если имя пакета совпадает с последним элементом пути,
// с алиасом — иначе.
func (r *importRegistry) imports() []string {
	out := make([]string, 0, len(r.order))
	for _, path := range r.order {
		name := r.names[path]
		if name == path[strings.LastIndex(path, "/")+1:] {
			out = append(out, "\""+path+"\"")
		} else {
			out = append(out, name+" \""+path+"\"")
		}
	}
	return out
}

// classify составляет план операторов для сброса значения типа t,
// доступного по выражению path.
func classify(ctx *classifyCtx, t types.Type, path string) []node {
	// Именованный тип: помеченный или с готовым методом Reset() сбрасывается
	// вызовом метода — независимо от пакета объявления.
	if named, ok := t.(*types.Named); ok {
		if ctx.marked[named] || hasResetMethod(named) {
			return []node{{kind: nodeCall, path: path}}
		}
	}

	switch u := t.Underlying().(type) {
	case *types.Basic:
		return []node{{kind: nodeZero, path: path, zero: zeroLiteral(u)}}
	case *types.Slice:
		return []node{{kind: nodeSlice, path: path}}
	case *types.Map:
		return []node{{kind: nodeMap, path: path}}
	case *types.Pointer:
		return classifyPointer(ctx, u, path)
	case *types.Struct:
		return classifyStruct(ctx, t, u, path)
	case *types.Array:
		return []node{{
			kind: nodeZero,
			path: path,
			zero: types.TypeString(t, ctx.registry.qualifier()) + "{}",
		}}
	case *types.Interface:
		if resetMethod(u) != nil {
			return []node{{kind: nodeCall, path: path}}
		}
		return []node{{kind: nodeSkip, note: "интерфейс без метода Reset() — сброс не генерируется"}}
	case *types.Chan:
		return []node{{kind: nodeSkip, note: "канал — сброс не генерируется"}}
	case *types.Signature:
		return []node{{kind: nodeSkip, note: "функция — сброс не генерируется"}}
	}
	// Недостижимо: перечислены все виды типов, допустимые в полях структур.
	return []node{{kind: nodeSkip, note: "тип не поддержан — сброс не генерируется"}}
}

// classifyPointer обрабатывает поле-указатель: все операторы сброса
// оказываются внутри проверки на nil. Указатель на структуру с Reset()
// вызывает метод напрямую (ae.child.Reset()), остальным типам к пути
// добавляется разыменование.
func classifyPointer(ctx *classifyCtx, ptr *types.Pointer, path string) []node {
	elem := ptr.Elem()
	if named, ok := elem.(*types.Named); ok && (ctx.marked[named] || hasResetMethod(named)) {
		return []node{{
			kind: nodeIf,
			cond: path + " != nil",
			body: []node{{kind: nodeCall, path: path}},
		}}
	}
	return []node{{
		kind: nodeIf,
		cond: path + " != nil",
		body: classify(ctx, elem, "*"+path),
	}}
}

// classifyStruct обрабатывает поле структурного типа: структуры своего
// пакета, включая анонимные, сбрасываются рекурсивным обходом полей;
// анонимные структуры чужих пакетов (через экспортируемые алиасы) — так же,
// с проверкой экспортируемости полей; чужие именованные — присваиванием
// нулевого значения целиком (zero-assign).
func classifyStruct(ctx *classifyCtx, t types.Type, st *types.Struct, path string) []node {
	named, hasNamed := t.(*types.Named)
	if !hasNamed {
		// Анонимная структура: у безымянного типа нет имени для импорта, поэтому
		// из чужого пакета она доступна только через экспортируемый алиас.
		// Рекурсия безопасна: безымянный тип не может содержать ссылку сам
		// на себя, а зацикливание через именованные типы отсекается проверкой
		// ctx.stack в ветке своего пакета; неэкспортируемые поля чужих
		// анонимных структур отсеивает classifyStructFields.
		return classifyStructFields(ctx, st, path)
	}
	if samePackage(named, ctx.pkg) {
		if ctx.stack[named] {
			return []node{{
				kind: nodeSkip,
				note: "циклическая ссылка на " + named.Obj().Name() + " — сброс не генерируется",
			}}
		}
		ctx.stack[named] = true
		nodes := classifyStructFields(ctx, st, path)
		delete(ctx.stack, named)
		return nodes
	}
	if named.Obj().Exported() {
		return []node{{
			kind: nodeZero,
			path: path,
			zero: types.TypeString(t, ctx.registry.qualifier()) + "{}",
		}}
	}
	return []node{{
		kind: nodeSkip,
		note: "неэкспортируемый тип чужого пакета — сброс не генерируется",
	}}
}

// classifyStructFields обходит поля структуры в порядке объявления и
// возвращает конкатенацию их планов. Для анонимных структур чужих пакетов
// (доступны через экспортируемые алиасы) неэкспортируемые поля пропускаются:
// обращение к ним из нашего пакета не соберётся.
func classifyStructFields(ctx *classifyCtx, st *types.Struct, path string) []node {
	var nodes []node
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Exported() && f.Pkg() != nil && f.Pkg().Path() != ctx.pkg.PkgPath {
			nodes = append(nodes, node{
				kind: nodeSkip,
				note: "неэкспортируемое поле анонимной структуры чужого пакета — сброс не генерируется",
			})
			continue
		}
		nodes = append(nodes, classify(ctx, f.Type(), path+"."+f.Name())...)
	}
	return nodes
}

// hasResetMethod сообщает, есть ли у именованного типа метод Reset() без
// параметров и результатов. Метод ищется в множестве методов указателя:
// сгенерированный вызов размещается на адресуемом значении.
func hasResetMethod(t *types.Named) bool {
	var pkg *types.Package
	if obj := t.Obj(); obj != nil {
		pkg = obj.Pkg()
	}
	sel := types.NewMethodSet(types.NewPointer(t)).Lookup(pkg, "Reset")
	if sel == nil {
		return false
	}
	sig, ok := sel.Obj().(*types.Func).Type().(*types.Signature)
	return ok && sig.Params().Len() == 0 && sig.Results().Len() == 0
}

// resetMethod ищет в интерфейсе метод Reset() без параметров и результатов.
func resetMethod(iface *types.Interface) *types.Func {
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		if m.Name() != "Reset" {
			continue
		}
		if sig, ok := m.Type().(*types.Signature); ok &&
			sig.Params().Len() == 0 && sig.Results().Len() == 0 {
			return m
		}
	}
	return nil
}

// zeroLiteral возвращает литерал нулевого значения базового типа:
// числа и комплексные — 0, строки — "", булевы — false, UnsafePointer — nil.
func zeroLiteral(b *types.Basic) string {
	switch {
	case b.Info()&types.IsBoolean != 0:
		return "false"
	case b.Info()&types.IsString != 0:
		return "\"\""
	case b.Kind() == types.UnsafePointer:
		return "nil"
	default:
		return "0"
	}
}

// samePackage сообщает, объявлен ли именованный тип в пакете owner.
func samePackage(t *types.Named, owner *packages.Package) bool {
	pkg := t.Obj().Pkg()
	return pkg != nil && pkg.Path() == owner.PkgPath
}

// receiverName строит имя приёмника метода: первая буква имени типа
// переводится в нижний регистр (AuditEvent -> auditEvent). Если тип
// неэкспортируемый, имя приёмника совпадает с именем типа — коллизий
// это не создаёт.
func receiverName(typeName string) string {
	r := []rune(typeName)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// buildPlan составляет план метода Reset() для помеченной структуры.
// recv — имя приёмника, зарезервированное в реестре импортов до классификации.
func buildPlan(ctx *classifyCtx, t *markedStruct, recv string) (*structPlan, error) {
	obj, ok := t.pkg.TypesInfo.Defs[t.spec.Name].(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("не найдено объявление типа %s (%s)", t.spec.Name.Name, t.pos)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("тип %s не является именованным (%s)", t.spec.Name.Name, t.pos)
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("тип %s не является структурой (%s)", t.spec.Name.Name, t.pos)
	}
	plan := &structPlan{
		pkg:  t.pkg,
		spec: t.spec,
		pos:  t.pos,
		recv: recv,
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		plan.fields = append(plan.fields, &fieldEntry{
			field: f,
			root:  classify(ctx, f.Type(), plan.recv+"."+f.Name()),
		})
	}
	return plan, nil
}

// methodData — данные для рендера метода Reset().
type methodData struct {
	Recv string // имя приёмника
	Type string // имя типа
	Body string // отрендеренное тело метода, с завершающим переводом строки
}

// fileData — данные для рендера файла reset.gen.go.
type fileData struct {
	Package string   // имя пакета
	Imports []string // импорты в готовом виде: с кавычками, с алиасами где нужно
	Methods string   // отрендеренные методы Reset(), разделённые пустой строкой
}

// methodTemplate — каркас метода Reset(): godoc, приёмник, тело из плана.
var methodTemplate = template.Must(template.New("method").Parse(
	`// Reset сбрасывает состояние объекта к начальным значениям.
// Сгенерировано утилитой cmd/reset по маркеру // generate:reset;.
func ({{.Recv}} *{{.Type}}) Reset() {
{{.Body}}}`))

// fileTemplate — каркас файла reset.gen.go: шапка, пакет, импорты
// (с алиасами из реестра), методы.
var fileTemplate = template.Must(template.New("file").Parse(
	`// Code generated by cmd/reset. DO NOT EDIT.
//
// Сгенерировано утилитой cmd/reset из структур с маркером // generate:reset;.
// Запуск: go run ./cmd/reset .
// Ручные правки будут перезаписаны при следующем прогоне.

package {{.Package}}
{{if .Imports}}
import (
{{range .Imports}}	{{.}}
{{end}})
{{end}}
{{.Methods}}
`))

// renderBody разворачивает план в код Go: каждый узел плана становится
// оператором с заданным отступом, условные блоки вкладываются друг в друга.
func renderBody(nodes []node, indent string) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.kind {
		case nodeZero:
			b.WriteString(indent + n.path + " = " + n.zero + "\n")
		case nodeSlice:
			b.WriteString(indent + n.path + " = " + n.path + "[:0]\n")
		case nodeMap:
			b.WriteString(indent + "clear(" + n.path + ")\n")
		case nodeCall:
			b.WriteString(indent + n.path + ".Reset()\n")
		case nodeIf:
			b.WriteString(indent + "if " + n.cond + " {\n")
			b.WriteString(renderBody(n.body, indent+"\t"))
			b.WriteString(indent + "}\n")
		case nodeSkip:
			b.WriteString(indent + "// " + n.note + "\n")
		}
	}
	return b.String()
}

// renderMethod рендерит один метод Reset(): каркас из methodTemplate,
// тело — развёртка плана.
func renderMethod(plan *structPlan) (string, error) {
	var body strings.Builder
	for _, fe := range plan.fields {
		body.WriteString(renderBody(fe.root, "\t"))
	}
	var buf bytes.Buffer
	err := methodTemplate.Execute(&buf, methodData{
		Recv: plan.recv,
		Type: plan.spec.Name.Name,
		Body: body.String(),
	})
	if err != nil {
		return "", fmt.Errorf("ошибка рендера метода для %s: %w", plan.spec.Name.Name, err)
	}
	return buf.String(), nil
}

// renderFile рендерит полное содержимое reset.gen.go пакета и прогоняет его
// через gofmt. При ошибке форматирования сгенерированный текст включается
// в сообщение об ошибке — так легко найти виноватый фрагмент.
func renderFile(pkg *packages.Package, imports []string, plans []*structPlan) ([]byte, error) {
	methods := make([]string, 0, len(plans))
	for _, plan := range plans {
		m, err := renderMethod(plan)
		if err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	var buf bytes.Buffer
	err := fileTemplate.Execute(&buf, fileData{
		Package: pkg.Name,
		Imports: imports,
		Methods: strings.Join(methods, "\n\n"),
	})
	if err != nil {
		return nil, fmt.Errorf("ошибка рендера %s: %w", genFileName, err)
	}
	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("ошибка форматирования %s:\n%s", genFileName, buf.String())
	}
	return src, nil
}

// genFilePath возвращает путь к файлу reset.gen.go пакета: файл живёт
// в каталоге файлов пакета. У пакета без Go-файлов (например, внешнего
// тестового пакета foo_test) генерации нет.
func genFilePath(pkg *packages.Package) (string, bool) {
	if len(pkg.GoFiles) == 0 {
		return "", false
	}
	return filepath.Join(filepath.Dir(pkg.GoFiles[0]), genFileName), true
}

// writeGenFile записывает отрендеренное содержимое на диск, если оно
// отличается от существующего: кеш компилятора не сбрасывается зря.
func writeGenFile(path string, methodCount int, src []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, src) {
		fmt.Printf("без изменений %s\n", path)
		return nil
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return fmt.Errorf("ошибка записи %s: %w", path, err)
	}
	fmt.Printf("сгенерирован %s (методов: %d)\n", path, methodCount)
	return nil
}

// removeStaleGenFile удаляет файл reset.gen.go пакета без помеченных
// структур: файл остался от прежних прогонов, когда маркеры сняли.
// Отсутствие файла ошибкой не считается.
func removeStaleGenFile(pkg *packages.Package) error {
	path, ok := genFilePath(pkg)
	if !ok {
		return nil
	}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("не удалось проверить существование %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("не удалось удалить устаревший %s: %w", path, err)
	}
	fmt.Printf("удалён устаревший %s\n", path)
	return nil
}

// run — основная фаза работы генератора: загрузка пакетов, сбор структур
// с маркером, планирование методов Reset() и генерация файлов reset.gen.go.
// Структуры группируются по пакетам: reset.gen.go один на пакет. Пакеты
// главного модуля без помеченных структур избавляются от устаревших файлов
// генерации.
func run(cfg *config) error {
	pkgs, err := loadPackages(cfg.root)
	if err != nil {
		return err
	}

	// Пакеты главного модуля в детерминированном порядке: отчёт прогона
	// не зависит от порядка загрузки пакетов.
	mainPkgs := make([]*packages.Package, 0, len(pkgs))
	for _, pkg := range pkgs {
		// Интересуют только пакеты главного модуля: синтаксис зависимостей
		// (стандартная библиотека, внешние пакеты) генератору не нужен.
		if pkg.Module == nil || !pkg.Module.Main {
			continue
		}
		mainPkgs = append(mainPkgs, pkg)
	}
	sort.Slice(mainPkgs, func(i, j int) bool {
		return mainPkgs[i].PkgPath < mainPkgs[j].PkgPath
	})

	byPkg := make(map[*packages.Package][]*markedStruct)
	var targets []*markedStruct
	for _, pkg := range mainPkgs {
		structs := collectStructs(pkg)
		if len(structs) > 0 {
			byPkg[pkg] = structs
			targets = append(targets, structs...)
		}
	}

	// Реестр помеченных типов всего проекта: поле любого пакета, ссылающееся
	// на помеченный тип, классифицируется как вызов Reset() — поведение
	// классификации не зависит от того, сгенерированы ли уже методы.
	marked := make(map[*types.Named]bool)
	for _, t := range targets {
		obj, ok := t.pkg.TypesInfo.Defs[t.spec.Name].(*types.TypeName)
		if !ok {
			return fmt.Errorf("не найдено объявление типа %s (%s)", t.spec.Name.Name, t.pos)
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			return fmt.Errorf("тип %s не является именованным (%s)", t.spec.Name.Name, t.pos)
		}
		marked[named] = true
	}

	for _, pkg := range mainPkgs {
		structs := byPkg[pkg]
		if len(structs) == 0 {
			if err := removeStaleGenFile(pkg); err != nil {
				return err
			}
			continue
		}
		// Контекст классификации общий на пакет: импорты reset.gen.go
		// собираются согласованно для всех структур пакета.
		ctx := &classifyCtx{
			pkg:      pkg,
			marked:   marked,
			registry: newImportRegistry(pkg.PkgPath),
			stack:    make(map[*types.Named]bool),
		}
		// Имена приёмников резервируются до классификации: они живут
		// в одном файле с импортами, и алиасы пакетов не должны совпадать
		// с идентификаторами из кода. Если имя занято, fallback — само имя
		// типа, затем числовой суффикс.
		recvs := make([]string, len(structs))
		for i, t := range structs {
			name := t.spec.Name.Name
			recv := receiverName(name)
			if ctx.registry.used[recv] && !ctx.registry.used[name] {
				recv = name
			}
			for n := 2; ctx.registry.used[recv]; n++ {
				recv = name + strconv.Itoa(n)
			}
			ctx.registry.used[recv] = true
			recvs[i] = recv
		}
		plans := make([]*structPlan, 0, len(structs))
		for i, t := range structs {
			plan, err := buildPlan(ctx, t, recvs[i])
			if err != nil {
				return err
			}
			plans = append(plans, plan)
		}
		// Порядок методов в файле детерминирован: по позиции объявления.
		sort.Slice(plans, func(i, j int) bool {
			a, b := plans[i].pos, plans[j].pos
			if a.Filename != b.Filename {
				return a.Filename < b.Filename
			}
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Column < b.Column
		})
		src, err := renderFile(pkg, ctx.registry.imports(), plans)
		if err != nil {
			return err
		}
		path, ok := genFilePath(pkg)
		if !ok {
			return fmt.Errorf("в пакете %s нет файлов", pkg.PkgPath)
		}
		if err := writeGenFile(path, len(plans), src); err != nil {
			return err
		}
	}
	return nil
}

// loadPackages загружает все пакеты проекта от корня. Нужны синтаксис файлов
// (NeedSyntax — поиск маркера), информация о типе (NeedTypes и NeedTypesInfo —
// классификация полей структур по типу) и сведения о модулях (NeedModule —
// фильтрация пакетов главного модуля).
func loadPackages(root string) ([]*packages.Package, error) {
	pkgs, err := packages.Load(&packages.Config{
		Dir: root,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule,
	}, "./...")
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки пакетов: %w", err)
	}
	var errs []packages.Error
	for _, pkg := range pkgs {
		errs = append(errs, pkg.Errors...)
	}
	if len(errs) > 0 {
		var msg strings.Builder
		for _, e := range errs {
			msg.WriteString("\n\t" + e.Error())
		}
		return nil, fmt.Errorf("ошибки загрузки пакетов:%s", msg.String())
	}
	return pkgs, nil
}

func main() {
	cfg := &config{}

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		_, _ = out.Write([]byte("использование: reset [корень]\n\n" +
			"Генерирует методы Reset() для структур с маркером // generate:reset;.\n" +
			"корень — директория сканирования (по умолчанию текущая).\n"))
	}
	flag.Parse()

	if flag.NArg() > 1 {
		flag.Usage()
		log.Fatal("ожидается не более одного позиционного аргумента")
	}
	cfg.root = flag.Arg(0)
	if cfg.root == "" {
		cfg.root = "."
	}

	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}
