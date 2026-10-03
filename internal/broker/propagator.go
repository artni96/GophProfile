package broker

type Propagator map[string]interface{}

func (p Propagator) Get(key string) string {
	if v, ok := p[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (p Propagator) Set(key, value string) {
	p[key] = value
}

func (p Propagator) Keys() []string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	return keys
}
