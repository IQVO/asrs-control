package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// StorageMapRepo is the pgx ports.StorageMaps.
type StorageMapRepo struct{ pool *pgxpool.Pool }

func NewStorageMapRepo(pool *pgxpool.Pool) *StorageMapRepo { return &StorageMapRepo{pool: pool} }

type slotJSON struct {
	Aisle int    `json:"a"`
	Level int    `json:"l"`
	Slot  int    `json:"s"`
	Depth int    `json:"d"`
	Value string `json:"v"`
}

// snapshotJSON is the stored shape of storagemap.Snapshot. ReservedFor (the container each
// reserved position is held for, Snapshot.ReservedContainer) is an addition to the brief's
// shape: Rehydrate refuses a reserved position without it. Rows written without it decode to an
// empty map, which Rehydrate then rejects loudly rather than guessing.
type snapshotJSON struct {
	Occupied    []slotJSON        `json:"occupied"`
	Reserved    []slotJSON        `json:"reserved"`
	ReservedFor []slotJSON        `json:"reserved_for"`
	Retrieving  map[string]string `json:"retrieving"`
	Moving      map[string]string `json:"moving"`
	Presented   map[string]string `json:"presented"`
}

func slots(m map[layout.Position]string) []slotJSON {
	out := make([]slotJSON, 0, len(m))
	for p, v := range m {
		out = append(out, slotJSON{Aisle: p.Aisle, Level: p.Level, Slot: p.Slot, Depth: p.Depth, Value: v})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Level != b.Level {
			return a.Level < b.Level
		}
		if a.Slot != b.Slot {
			return a.Slot < b.Slot
		}
		return a.Depth < b.Depth
	})
	return out
}

func unslots(in []slotJSON) map[layout.Position]string {
	out := map[layout.Position]string{}
	for _, s := range in {
		out[layout.Position{Aisle: s.Aisle, Level: s.Level, Slot: s.Slot, Depth: s.Depth}] = s.Value
	}
	return out
}

func encodeSnapshot(s storagemap.Snapshot) ([]byte, []string, error) {
	b, err := json.Marshal(snapshotJSON{Occupied: slots(s.Occupied), Reserved: slots(s.Reserved),
		ReservedFor: slots(s.ReservedContainer),
		Retrieving:  s.Retrieving, Moving: s.Moving, Presented: s.Presented})
	containers := make([]string, 0, len(s.Occupied))
	for _, c := range s.Occupied {
		containers = append(containers, c)
	}
	sort.Strings(containers)
	return b, containers, err
}

func decodeSnapshot(aisle int, b []byte) (storagemap.Snapshot, error) {
	var j snapshotJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return storagemap.Snapshot{}, fmt.Errorf("decode storage map: %w", err)
	}
	return storagemap.Snapshot{Aisle: aisle, Occupied: unslots(j.Occupied), Reserved: unslots(j.Reserved),
		ReservedContainer: unslots(j.ReservedFor),
		Retrieving:        j.Retrieving, Moving: j.Moving, Presented: j.Presented}, nil
}

// Ensure creates the missing aisle rows and refuses a stored map the layout cannot hold.
func (r *StorageMapRepo) Ensure(ctx context.Context, l layout.Layout) error {
	for n := 1; n <= l.Aisles; n++ {
		if err := r.ensureAisle(ctx, l, n); err != nil {
			return err
		}
	}
	var stranded int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM storage_maps WHERE asrs_id = $1 AND aisle > $2 AND cardinality(containers) > 0`,
		l.ID, l.Aisles).Scan(&stranded); err != nil {
		return err
	}
	if stranded > 0 {
		return fmt.Errorf("%s: %d aisle(s) beyond the layout file still hold containers", l.ID, stranded)
	}
	return nil
}

func (r *StorageMapRepo) ensureAisle(ctx context.Context, l layout.Layout, n int) error {
	b, _, err := encodeSnapshot(storagemap.New(l, n).Snapshot())
	if err != nil {
		return err
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO storage_maps (asrs_id, aisle, snapshot) VALUES ($1, $2, $3)
		ON CONFLICT (asrs_id, aisle) DO NOTHING`, l.ID, n, b); err != nil {
		return fmt.Errorf("ensure storage map %s aisle %d: %w", l.ID, n, err)
	}
	if _, err := r.Get(ctx, l, n); err != nil {
		return fmt.Errorf("the stored map of %s aisle %d does not fit the layout file: %w", l.ID, n, err)
	}
	return nil
}

func (r *StorageMapRepo) Lock(ctx context.Context, l layout.Layout, aisle int) (*storagemap.Map, error) {
	return r.load(ctx, l, aisle, " FOR UPDATE")
}

func (r *StorageMapRepo) Get(ctx context.Context, l layout.Layout, aisle int) (*storagemap.Map, error) {
	return r.load(ctx, l, aisle, "")
}

func (r *StorageMapRepo) load(ctx context.Context, l layout.Layout, aisle int, lock string) (*storagemap.Map, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT snapshot FROM storage_maps WHERE asrs_id = $1 AND aisle = $2`+lock, l.ID, aisle).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("storage map %s aisle %d does not exist (boot runs Ensure)", l.ID, aisle)
	}
	if err != nil {
		return nil, fmt.Errorf("load storage map: %w", err)
	}
	s, err := decodeSnapshot(aisle, b)
	if err != nil {
		return nil, err
	}
	return storagemap.Rehydrate(l, s)
}

func (r *StorageMapRepo) Save(ctx context.Context, asrsID string, m *storagemap.Map) error {
	b, containers, err := encodeSnapshot(m.Snapshot())
	if err != nil {
		return err
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `UPDATE storage_maps SET snapshot = $3, containers = $4, updated_at = now()
		WHERE asrs_id = $1 AND aisle = $2`, asrsID, m.Aisle(), b, containers)
	if err != nil {
		return fmt.Errorf("save storage map: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("save storage map %s aisle %d: no such row", asrsID, m.Aisle())
	}
	return nil
}

func (r *StorageMapRepo) Find(ctx context.Context, asrsID, containerID string) (int, bool, error) {
	var aisle int
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT aisle FROM storage_maps WHERE asrs_id = $1 AND $2 = ANY(containers) LIMIT 1`,
		asrsID, containerID).Scan(&aisle)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find container: %w", err)
	}
	return aisle, true, nil
}
