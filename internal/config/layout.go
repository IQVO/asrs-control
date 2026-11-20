// Package config reads asrs-control's AS/RS layout file (ADR 0002). Its field names are the
// emulator world's `asrs` block, so an operator can copy that block verbatim; the
// emulator-only fields (timing, port nodeId) are accepted and ignored.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// DefaultCost applies to an entry without a cost block: one unit per level and per slot.
var DefaultCost = layout.Cost{PerLevel: 1, PerSlot: 1}

type layoutFile struct {
	Asrs []asrsEntry `yaml:"asrs"`
}

type asrsEntry struct {
	ID         string      `yaml:"id"`
	Aisles     int         `yaml:"aisles"`
	Levels     int         `yaml:"levels"`
	Slots      int         `yaml:"slots"`
	DoubleDeep bool        `yaml:"doubleDeep"`
	Cost       *costEntry  `yaml:"cost"`
	Ports      []portEntry `yaml:"ports"`
	// Emulator-only timing, ignored.
	BaseSeconds     float64 `yaml:"baseSeconds"`
	PerLevelSeconds float64 `yaml:"perLevelSeconds"`
	PerSlotSeconds  float64 `yaml:"perSlotSeconds"`
}

type costEntry struct {
	PerLevel float64 `yaml:"perLevel"`
	PerSlot  float64 `yaml:"perSlot"`
}

type portEntry struct {
	ID    string `yaml:"id"`
	Aisle int    `yaml:"aisle"`
	Kind  string `yaml:"kind"`
	// NodeID is the emulator's VDA 5050 node of the port, ignored here.
	NodeID string `yaml:"nodeId"`
}

func (e asrsEntry) toLayout() layout.Layout {
	l := layout.Layout{ID: e.ID, Aisles: e.Aisles, Levels: e.Levels, Slots: e.Slots, DoubleDeep: e.DoubleDeep, Cost: DefaultCost}
	if e.Cost != nil {
		l.Cost = layout.Cost{PerLevel: e.Cost.PerLevel, PerSlot: e.Cost.PerSlot}
	}
	for _, p := range e.Ports {
		l.Ports = append(l.Ports, layout.Port{ID: p.ID, Aisle: p.Aisle, Kind: layout.PortKind(p.Kind)})
	}
	return l
}

// ParseLayouts decodes and validates a layout file. Unknown fields are refused.
func ParseLayouts(b []byte) ([]layout.Layout, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var f layoutFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("layout file: %w", err)
	}
	if len(f.Asrs) == 0 {
		return nil, errors.New("layout file: no asrs entry")
	}
	seen := map[string]bool{}
	out := make([]layout.Layout, 0, len(f.Asrs))
	for _, e := range f.Asrs {
		l := e.toLayout()
		if err := l.Validate(); err != nil {
			return nil, fmt.Errorf("layout file: %w", err)
		}
		if seen[l.ID] {
			return nil, fmt.Errorf("layout file: asrs %s is declared twice", l.ID)
		}
		seen[l.ID] = true
		out = append(out, l)
	}
	return out, nil
}

// LoadLayouts reads and parses the layout file at path.
func LoadLayouts(path string) ([]layout.Layout, error) {
	b, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the layout path is operator-supplied configuration
	if err != nil {
		return nil, fmt.Errorf("layout file: %w", err)
	}
	return ParseLayouts(b)
}
