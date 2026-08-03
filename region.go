package ikkyoku

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRegion は "x,y,width,height" 形式の文字列を Region に変換する。
// CLI フラグ（-region）からの入力を想定している。
func ParseRegion(s string) (Region, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return Region{}, fmt.Errorf("ikkyoku: 領域の指定が不正です（x,y,width,height の形式で指定すること）: %q", s)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return Region{}, fmt.Errorf("ikkyoku: 領域の指定が不正です: %q: %w", s, err)
		}
		vals[i] = v
	}
	r := Region{X: vals[0], Y: vals[1], Width: vals[2], Height: vals[3]}
	if !r.Valid() {
		return Region{}, fmt.Errorf("ikkyoku: 幅・高さは正の値である必要があります: %q", s)
	}
	return r, nil
}
