package secrets

import "context"

// learner teaches a redactor every value that passes through a store.
type learner struct {
	Store
	learn func(string)
}

// Learn wraps s so that each value read or written is passed to learn, which
// is the redactor's Add (github-integration R6.3: the logger redacts known
// secret values). The Store API is unchanged.
func Learn(s Store, learn func(string)) Store {
	if learn == nil {
		return s
	}
	return &learner{Store: s, learn: learn}
}

// Get implements Store.
func (l *learner) Get(ctx context.Context, key Key) ([]byte, error) {
	v, err := l.Store.Get(ctx, key)
	if err == nil {
		l.learn(string(v))
	}
	return v, err
}

// Put implements Store.
func (l *learner) Put(ctx context.Context, key Key, value []byte) error {
	l.learn(string(value))
	return l.Store.Put(ctx, key, value)
}
