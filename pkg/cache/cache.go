package cache

type Cacheable interface {
	Key() string
	Value() string
	SetValue(string)
}

type Cache[T Cacheable] interface {
	Set(T)
	Get(T)
}
