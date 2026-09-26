package storage

// globMatch — glob в стиле Redis KEYS: *, ?, [abc], [^a], [a-z], \x.
// В отличие от filepath.Match, '*' матчит и '/'.
// Жадный алгоритм с одной точкой отката: O(len(pattern)*len(str)) в худшем случае.
func globMatch(pattern, str string) bool {
	px, sx := 0, 0
	nextPx, nextSx := 0, 0
	for px < len(pattern) || sx < len(str) {
		if px < len(pattern) {
			if pattern[px] == '*' {
				nextPx, nextSx = px, sx+1
				px++
				continue
			}
			if sx < len(str) {
				if ok, width := matchOne(pattern[px:], str[sx]); ok {
					px += width
					sx++
					continue
				}
			}
		}
		if 0 < nextSx && nextSx <= len(str) {
			px, sx = nextPx, nextSx
			continue
		}
		return false
	}
	return true
}

// matchOne сравнивает один элемент паттерна (не '*') с символом ch.
// Возвращает результат и ширину элемента в паттерне.
func matchOne(p string, ch byte) (bool, int) {
	switch p[0] {
	case '?':
		return true, 1
	case '\\':
		if len(p) >= 2 {
			return p[1] == ch, 2
		}
		return ch == '\\', 1
	case '[':
		i := 1
		neg := false
		if i < len(p) && p[i] == '^' {
			neg = true
			i++
		}
		match := false
		for i < len(p) && p[i] != ']' {
			switch {
			case p[i] == '\\' && i+1 < len(p):
				if p[i+1] == ch {
					match = true
				}
				i += 2
			case i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']':
				lo, hi := p[i], p[i+2]
				if lo > hi {
					lo, hi = hi, lo
				}
				if ch >= lo && ch <= hi {
					match = true
				}
				i += 3
			default:
				if p[i] == ch {
					match = true
				}
				i++
			}
		}
		if i < len(p) {
			i++ // ']'
		}
		return match != neg, i
	default:
		return p[0] == ch, 1
	}
}
