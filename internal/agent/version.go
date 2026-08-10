package agent

import "strings"

func versionEqual(a, b string) bool {
	return normalizeVer(a) == normalizeVer(b)
}

// versionLess reports whether a is strictly older than b.
// Prefers semver (major.minor.patch); otherwise lexicographic (ISO build stamps).
func versionLess(a, b string) bool {
	a, b = normalizeVer(a), normalizeVer(b)
	if a == "" {
		return b != ""
	}
	if b == "" {
		return false
	}
	ap, aOK := parseSemver(a)
	bp, bOK := parseSemver(b)
	if aOK && bOK {
		for i := 0; i < 3; i++ {
			if ap[i] < bp[i] {
				return true
			}
			if ap[i] > bp[i] {
				return false
			}
		}
		return false
	}
	return a < b
}

func normalizeVer(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return out, false
	}
	for i := 0; i < len(parts); i++ {
		n := 0
		if parts[i] == "" {
			return out, false
		}
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				return out, false
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out, true
}
