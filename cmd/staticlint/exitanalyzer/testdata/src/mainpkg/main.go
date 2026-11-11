// Пакет mainpkg — сценарии для анализатора exitinmain.
package main

import (
	"os"

	xos "os"
)

// helper — обычная функция пакета main: os.Exit в ней разрешён.
func helper() {
	os.Exit(1)
}

func main() {
	helper()

	func() {
		os.Exit(2) // want `прямой вызов os.Exit`
	}()

	os.Exit(3)  // want `прямой вызов os.Exit`
	xos.Exit(4) // want `прямой вызов os.Exit`

	var f func(int)
	f = os.Exit
	f(5) // вызов через переменную — не прямой, диагностики нет
}
