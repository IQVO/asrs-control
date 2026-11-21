package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IQVO/asrs-control/internal/config"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

const emulatorBlock = `
asrs:
  - id: ASRS1
    aisles: 2
    levels: 8
    slots: 40
    doubleDeep: true
    baseSeconds: 6
    perLevelSeconds: 1.5
    perSlotSeconds: 0.2
    ports:
      - {id: IN1, aisle: 1, kind: INPUT, nodeId: ASRS1-IN1}
      - {id: OUT1, aisle: 1, kind: OUTPUT, nodeId: ASRS1-OUT1}
      - {id: IN2, aisle: 2, kind: INPUT, nodeId: ASRS1-IN2}
      - {id: OUT2, aisle: 2, kind: OUTPUT, nodeId: ASRS1-OUT2}
`

func TestTheEmulatorAsrsBlockLoadsWithTheDefaultCost(t *testing.T) {
	ls, err := config.ParseLayouts([]byte(emulatorBlock))
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 {
		t.Fatalf("got %d layouts, want 1", len(ls))
	}
	l := ls[0]
	if l.ID != "ASRS1" || l.Aisles != 2 || l.Levels != 8 || l.Slots != 40 || !l.DoubleDeep || len(l.Ports) != 4 {
		t.Fatalf("layout = %+v", l)
	}
	if l.Cost != config.DefaultCost {
		t.Errorf("Cost = %+v, want the default %+v", l.Cost, config.DefaultCost)
	}
	if p, _ := l.Port("OUT2"); p.Kind != layout.PortOutput || p.Aisle != 2 {
		t.Errorf("OUT2 = %+v", p)
	}
}

func TestAnExplicitCostWins(t *testing.T) {
	src := strings.Replace(emulatorBlock, "    doubleDeep: true\n", "    doubleDeep: true\n    cost: {perLevel: 1.5, perSlot: 0.2}\n", 1)
	ls, err := config.ParseLayouts([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if ls[0].Cost != (layout.Cost{PerLevel: 1.5, PerSlot: 0.2}) {
		t.Errorf("Cost = %+v", ls[0].Cost)
	}
}

func TestTheLoaderRefuses(t *testing.T) {
	cases := map[string]string{
		"an unknown field":  strings.Replace(emulatorBlock, "slots: 40", "slots: 40\n    shuttles: 2", 1),
		"no asrs entry":     "asrs: []\n",
		"an empty file":     "",
		"an invalid layout": strings.Replace(emulatorBlock, "aisles: 2", "aisles: 0", 1),
		"a duplicate asrs":  emulatorBlock + strings.TrimPrefix(emulatorBlock, "\nasrs:\n"),
		"malformed yaml":    "asrs: [",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := config.ParseLayouts([]byte(src)); err == nil {
				t.Fatal("ParseLayouts accepted it")
			}
		})
	}
}

func TestLoadLayoutsReadsTheExampleFile(t *testing.T) {
	ls, err := config.LoadLayouts(filepath.Join("..", "..", "config", "layout.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].ID != "ASRS1" || ls[0].TotalPositions() != 1280 {
		t.Fatalf("example layout = %+v", ls)
	}
	if _, err := config.LoadLayouts(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("a missing file loaded")
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("asrs: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadLayouts(bad); err == nil {
		t.Fatal("a malformed file loaded")
	}
}
