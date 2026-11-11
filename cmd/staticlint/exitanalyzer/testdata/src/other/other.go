// Пакет other — не пакет main: анализатор молчит, даже если os.Exit
// вызывается в функции с именем main (это обычная функция).
package other

import "os"

func main() {
	os.Exit(1)
}
