package cache

type Cacheable interface {
	HashKey() string
	Base64Value() string
	SetValueFromBase64(string)
	Value() string
}

type Cache[T Cacheable] interface {
	Set(T)
	Get(T) *T
}
