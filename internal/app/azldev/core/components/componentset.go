// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

// Represents a unique logical set of components. Maintains insertion order.
type ComponentSet struct {
	// The components, mapped from name to component config.
	components map[string]Component

	// Ordered slice of keys; allows us to preserve insertion order for the map.
	keys []string
}

// Constructs an empty ComponentSet.
func NewComponentSet() *ComponentSet {
	return &ComponentSet{
		components: make(map[string]Component),
		keys:       []string{},
	}
}

// Adds `component` to the component set, associating it with `name`. If a component with
// the same name is already present, it's replaced with `component`. Insertion order
// is maintained for stable enumeration.
func (cs *ComponentSet) Add(component Component) {
	name := component.GetName()

	alreadyPresent := cs.Contains(name)

	cs.components[name] = component

	if !alreadyPresent {
		cs.keys = append(cs.keys, name)
	}
}

// Checks if a component called `name` is present in the set.
func (cs *ComponentSet) Contains(name string) bool {
	_, present := cs.components[name]

	return present
}

// Returns the number of unique components in the set.
func (cs *ComponentSet) Len() int {
	return len(cs.keys)
}

// Tries to retrieve the named component. If found, returns it; returns a clear
// indication of whether the component was present.
func (cs *ComponentSet) TryGet(name string) (Component, bool) {
	comp, present := cs.components[name]

	return comp, present
}

// Retrieves the names of the components in the set, in original insertion order.
func (cs *ComponentSet) Names() []string {
	return cs.keys
}

// Retrieves the components in the set, in original insertion order.
func (cs *ComponentSet) Components() []Component {
	components := make([]Component, 0, len(cs.keys))

	for _, name := range cs.keys {
		components = append(components, cs.components[name])
	}

	return components
}
