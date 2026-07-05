package imapsearch

import "testing"

func TestEqualUIDs(t *testing.T) {
	cases := []struct {
		a, b []uint32
		want bool
	}{
		{nil, nil, true},
		{[]uint32{1, 2}, []uint32{1, 2}, true},
		{[]uint32{1, 2}, []uint32{1, 3}, false},
		{[]uint32{1}, []uint32{1, 2}, false},
		{nil, []uint32{1}, false},
	}
	for _, c := range cases {
		if got := equalUIDs(c.a, c.b); got != c.want {
			t.Errorf("equalUIDs(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
