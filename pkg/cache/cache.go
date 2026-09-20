package cache

type Cacheable interface {
	hashKey() string
	base64Value() string
	setValueBase64(string)
	value() string
}

type Cache interface {
	Set(Cacheable)
	Get(Cacheable) *Cacheable
}
