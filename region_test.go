package ikkyoku

import "testing"

func TestParseRegion(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Region
		wantErr bool
	}{
		{"ok", "10,20,300,400", Region{X: 10, Y: 20, Width: 300, Height: 400}, false},
		{"negative origin (マルチモニタで許容)", "-100,0,640,480", Region{X: -100, Y: 0, Width: 640, Height: 480}, false},
		{"spaces", " 10 , 20 , 300 , 400 ", Region{X: 10, Y: 20, Width: 300, Height: 400}, false},
		{"too few parts", "10,20,300", Region{}, true},
		{"too many parts", "10,20,300,400,500", Region{}, true},
		{"not a number", "a,20,300,400", Region{}, true},
		{"zero width", "10,20,0,400", Region{}, true},
		{"negative height", "10,20,300,-1", Region{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRegion(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRegion(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("ParseRegion(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRegionString(t *testing.T) {
	r := Region{X: 1, Y: 2, Width: 3, Height: 4}
	if got, want := r.String(), "1,2,3,4"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	// String() の出力が ParseRegion で往復できること。
	got, err := ParseRegion(r.String())
	if err != nil {
		t.Fatalf("ParseRegion(%q) error = %v", r.String(), err)
	}
	if got != r {
		t.Errorf("round-trip: got %+v, want %+v", got, r)
	}
}

func TestRegionRect(t *testing.T) {
	r := Region{X: 10, Y: 20, Width: 300, Height: 400}
	rect := r.Rect()
	if rect.Min.X != 10 || rect.Min.Y != 20 || rect.Max.X != 310 || rect.Max.Y != 420 {
		t.Errorf("Rect() = %+v, unexpected", rect)
	}
}

func TestRegionValid(t *testing.T) {
	if !(Region{Width: 1, Height: 1}).Valid() {
		t.Error("1x1 should be valid")
	}
	if (Region{Width: 0, Height: 1}).Valid() {
		t.Error("0 width should be invalid")
	}
	if (Region{Width: 1, Height: 0}).Valid() {
		t.Error("0 height should be invalid")
	}
}
