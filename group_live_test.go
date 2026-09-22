package opcda

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLivePersistentRead(t *testing.T) {
	if os.Getenv("OPCDA_LIVE") != "1" {
		t.Skip("explicit read-only live test")
	}
	cfg := ServerConfig{
		Host:     os.Getenv("OPCDA_HOST"),
		Domain:   os.Getenv("OPCDA_DOMAIN"),
		Username: os.Getenv("OPCDA_USERNAME"),
		Password: os.Getenv("OPCDA_PASSWORD"),
		CLSID:    os.Getenv("OPCDA_CLSID"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	s, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closectx, done := cleanupContext()
		defer done()
		if err := s.Close(closectx); err != nil {
			t.Error(err)
		}
	}()
	g, err := s.AddGroup(ctx, "", 500*time.Millisecond, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("revised rate", g.RevisedUpdateRate())
	data, err := os.ReadFile(os.Getenv("OPCDA_ITEMS_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(data))
	if len(ids) != 1000 {
		t.Fatalf("expected 1000 test ItemIDs, got %d", len(ids))
	}
	start := time.Now()
	items, err := g.AddItems(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Error != nil {
			t.Fatalf("%s: %s", item.ItemID, item.Error)
		}
	}
	t.Log("AddItems", len(items), time.Since(start))
	for i := range 3 {
		start := time.Now()
		result, err := g.Read(ctx, SourceDevice)
		if err != nil {
			t.Fatal(err)
		}
		good, bad, failed := 0, 0, 0
		for _, r := range result {
			if r.Error != nil {
				failed++
			} else if QualityIsGood(r.Quality) {
				good++
			} else {
				bad++
			}
		}
		t.Log("Read", i, len(result), time.Since(start), "good", good, "bad", bad, "failed", failed)
		if len(result) != len(ids) {
			t.Fatal("wrong read count")
		}
	}
	closectx, done := cleanupContext()
	defer done()
	if err := g.Remove(closectx); err != nil {
		t.Fatal(err)
	}
}
