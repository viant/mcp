package collection

// OrderedMap is a generic collection type that holds items of type T indexed by string names
type OrderedMap[T any] struct {
	*SyncMap[string, T] // Holds a collection of prompts indexed by their names
	Keys                []string
}

// Add adds a new item to the collection, if the name already exists it will overwrite the existing item
func (c *OrderedMap[T]) Add(key string, item T) bool {
	_, has := c.Get(key)
	if !has {
		c.Put(key, item)
		c.Keys = append(c.Keys, key)
	}
	return false // return false if the item was already present, true if it was added
}

// Get retrieves an item from the collection by its name.
func (c *OrderedMap[T]) Get(name string) (T, bool) {
	if item, ok := c.Get(name); ok {
		return item, true
	}
	var t T
	return t, false
}

func NewOrderedMap[T any]() *OrderedMap[T] {
	// Initialize a new collection with an empty syncMap
	registry := NewSyncMap[string, T]()
	return &OrderedMap[T]{
		SyncMap: registry,
		Keys:    []string{},
	}
}
