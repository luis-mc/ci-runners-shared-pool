package load

import "testing"

// R5.16: the decision figure is the median of the last 5 samples; one spike
// does not decide (DL176).
func TestMedianIgnoresASpike(t *testing.T) {
	if got := Median([]float64{40, 41, 99, 40, 42}); got != 41 {
		t.Fatalf("median = %v, want 41", got)
	}
	if got := Median([]float64{10, 20}); got != 15 {
		t.Fatalf("even median = %v, want 15", got)
	}
	if Median(nil) != 0 {
		t.Fatal("empty median must be 0")
	}
}

func TestSamplerDecidesOnMedianOfLastFive(t *testing.T) {
	mem := func(avail int) string {
		return "MemTotal: 1000 kB\nMemAvailable: " + itoa(avail) + " kB\n"
	}
	p := linux(map[string]string{"/proc/stat": statA})
	s := NewSampler(p)
	// use %: 60, 60, 95 (spike), 60, 60 -> 60; a sixth 61 pushes the first out.
	for _, avail := range []int{400, 400, 50, 400, 400} {
		p.files["/proc/meminfo"] = mem(avail)
		s.Sample()
		p.tick(5e9)
	}
	r, _ := s.Latest()
	if r.MemoryPct != 60.0 {
		t.Fatalf("decided on %v, want the median 60 (spike ignored)", r.MemoryPct)
	}
	// Only the last Window samples count: five spikes in a row decide.
	for i := 0; i < Window; i++ {
		p.files["/proc/meminfo"] = mem(50)
		s.Sample()
	}
	if r, _ = s.Latest(); r.MemoryPct != 95.0 {
		t.Fatalf("after five high samples median = %v, want 95", r.MemoryPct)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
