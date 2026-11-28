package store

const dayMS = 24 * 60 * 60 * 1000

// unixMillis is the Unix time in milliseconds of a stored time, without the cost of time.Parse: ok is false when s is
// empty or has another layout.
func unixMillis(s string) (ms int64, ok bool) {
	// Both layouts are stored: the 24-byte one fmtTime writes, and the 20-byte one GitHub sends, without a fraction.
	switch {
	case len(s) == len(timeLayout) && s[19] == '.' && s[23] == 'Z':
	case len(s) == len("2006-01-02T15:04:05Z") && s[19] == 'Z':
	default:
		return 0, false
	}
	if s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':' {
		return 0, false
	}
	ok = true
	d := func(i, n int) (v int64) {
		for j := i; j < i+n; j++ {
			c := s[j]
			if c < '0' || c > '9' {
				ok = false
				return 0
			}
			v = v*10 + int64(c-'0')
		}
		return v
	}
	y, mo, day := d(0, 4), d(5, 2), d(8, 2)
	h, mi, sec := d(11, 2), d(14, 2), d(17, 2)
	var frac int64
	if len(s) == len(timeLayout) {
		frac = d(20, 3)
	}
	if !ok || mo < 1 || mo > 12 || day < 1 || day > 31 || h > 23 || mi > 59 || sec > 59 {
		return 0, false
	}
	// Days since 1970-01-01 of the civil date (the era algorithm, proleptic Gregorian).
	if mo <= 2 {
		y--
	}
	era := y / 400
	yoe := y - era*400
	mp := (mo + 9) % 12
	doy := (153*mp+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := era*146097 + doe - 719468
	return (((days*24+h)*60+mi)*60+sec)*1000 + frac, true
}

// unixMillisAny is unixMillis with time.Parse for another layout; ok is false for an empty or unreadable time.
func unixMillisAny(s string) (int64, bool) {
	if ms, ok := unixMillis(s); ok {
		return ms, true
	}
	if s == "" {
		return 0, false
	}
	t := parseTime(s)
	return t.UnixMilli(), !t.IsZero()
}

// secondsBetween is seconds without time.Parse for the times fmtTime wrote: the time from a to b, ok false when either is
// empty or b is before a.
func secondsBetween(a, b string) (float64, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	x, ok1 := unixMillis(a)
	y, ok2 := unixMillis(b)
	if !ok1 || !ok2 {
		return seconds(a, b)
	}
	return float64(y-x) / 1000, y >= x
}
