package base62

const base62Chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

var base62Set [128]bool

func init() {
	for _, c := range base62Chars {
		if c < 128 {
			base62Set[c] = true
		}
	}
}
