package ingest

// bounded is a map that forgets its oldest keys past a size. Not safe for
// concurrent use; the Recorder's mutex guards it.
type bounded[K comparable, V any] struct {
	max   int
	m     map[K]V
	order []K
}

func newBounded[K comparable, V any](max int) *bounded[K, V] {
	return &bounded[K, V]{max: max, m: map[K]V{}}
}

func (b *bounded[K, V]) get(k K) (V, bool) { v, ok := b.m[k]; return v, ok }

func (b *bounded[K, V]) put(k K, v V) {
	if _, ok := b.m[k]; !ok {
		b.order = append(b.order, k)
		if len(b.order) > b.max {
			drop := len(b.order) - b.max
			for _, old := range b.order[:drop] {
				delete(b.m, old)
			}
			b.order = append([]K(nil), b.order[drop:]...)
		}
	}
	b.m[k] = v
}

func (b *bounded[K, V]) del(k K) {
	if _, ok := b.m[k]; !ok {
		return
	}
	delete(b.m, k)
	for i, o := range b.order {
		if o == k {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
}
