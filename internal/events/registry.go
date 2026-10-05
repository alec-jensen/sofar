package events

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// Registry maps each event kind to the Go struct of its payload, per version,
// so old events keep decoding after a payload changes shape. The version is
// stored inside the payload as "_v".
type Registry struct {
	mu    sync.RWMutex
	kinds map[string]*kind
}

type kind struct {
	current int
	types   map[int]reflect.Type
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{kinds: map[string]*kind{}} }

// Register adds a payload struct for a kind at a version. The highest
// registered version is the one written by Encode. Register the zero value of
// the struct, for example Register("overlay.set_note", 1, NoteSet{}).
func (r *Registry) Register(kindName string, version int, example any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.kinds[kindName]
	if k == nil {
		k = &kind{types: map[int]reflect.Type{}}
		r.kinds[kindName] = k
	}
	t := reflect.TypeOf(example)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	k.types[version] = t
	if version > k.current {
		k.current = version
	}
}

// Known reports whether a kind is registered.
func (r *Registry) Known(kindName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.kinds[kindName]
	return ok
}

// Encode serializes a payload with its version. A nil payload is an empty
// object; a json.RawMessage is stored as given.
func (r *Registry) Encode(kindName string, payload any) ([]byte, error) {
	if payload == nil {
		return []byte(`{}`), nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		return raw, nil
	}
	if r == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kindName)
	}
	r.mu.RLock()
	k := r.kinds[kindName]
	r.mu.RUnlock()
	if k == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kindName)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("events: payload for %q must be a JSON object: %w", kindName, err)
	}
	obj["_v"] = json.RawMessage(fmt.Sprint(k.current))
	return json.Marshal(obj)
}

// Decode returns the payload as a pointer to the struct registered for the
// event's kind and stored version.
func (r *Registry) Decode(e Event) (any, error) {
	r.mu.RLock()
	k := r.kinds[e.Kind]
	r.mu.RUnlock()
	if k == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, e.Kind)
	}
	var head struct {
		V int `json:"_v"`
	}
	if err := json.Unmarshal(e.Payload, &head); err != nil {
		return nil, err
	}
	if head.V == 0 {
		head.V = 1
	}
	t, ok := k.types[head.V]
	if !ok {
		return nil, fmt.Errorf("events: %q has no payload version %d", e.Kind, head.V)
	}
	v := reflect.New(t).Interface()
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return nil, err
	}
	return v, nil
}
