package mongodbmodule

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestEnsureIndexPreservesOrderAndOptions(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	module := startTestModule(t, runtime)
	name, err := module.EnsureIndex(
		context.Background(),
		"players",
		bson.D{{Key: "server_id", Value: 1}, {Key: "level", Value: -1}},
		mongooptions.Index().SetName("server_level").SetSparse(true),
	)
	if err != nil || name == "" {
		t.Fatalf("EnsureIndex() = %q, %v", name, err)
	}
	document, ok := runtime.created[0].Keys.(bson.D)
	if !ok || document[0].Key != "server_id" || document[1].Key != "level" {
		t.Fatalf("index keys = %#v", runtime.created[0].Keys)
	}
	options := applyIndexOptions(t, runtime.created[0])
	if options.Name == nil || *options.Name != "server_level" || options.Sparse == nil || !*options.Sparse {
		t.Fatalf("index options = %#v", options)
	}
}

func TestUniqueAndTTLForceFinalInvariant(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	module := startTestModule(t, runtime)
	if _, err := module.EnsureUniqueIndex(
		context.Background(), "players", bson.D{{Key: "player_id", Value: 1}},
		mongooptions.Index().SetUnique(false),
	); err != nil {
		t.Fatal(err)
	}
	unique := applyIndexOptions(t, runtime.created[0])
	if unique.Unique == nil || !*unique.Unique {
		t.Fatalf("unique option = %#v", unique.Unique)
	}

	if _, err := module.EnsureTTLIndex(
		context.Background(), "sessions", "expire_at", 30*time.Minute,
		mongooptions.Index().SetExpireAfterSeconds(1),
	); err != nil {
		t.Fatal(err)
	}
	ttl := applyIndexOptions(t, runtime.created[1])
	if ttl.ExpireAfterSeconds == nil || *ttl.ExpireAfterSeconds != 1800 {
		t.Fatalf("TTL seconds = %v, want 1800", ttl.ExpireAfterSeconds)
	}
}

func TestEnsureIndexRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	module := startTestModule(t, newFakeRuntime())
	typedNil := (*mongooptions.IndexOptionsBuilder)(nil)
	tests := []struct {
		name string
		call func() error
	}{
		{name: "nil context", call: func() error { _, err := module.EnsureIndex(nil, "players", bson.D{{Key: "id", Value: 1}}); return err }},
		{name: "empty collection", call: func() error {
			_, err := module.EnsureIndex(context.Background(), "", bson.D{{Key: "id", Value: 1}})
			return err
		}},
		{name: "empty keys", call: func() error { _, err := module.EnsureIndex(context.Background(), "players", bson.D{}); return err }},
		{name: "empty key", call: func() error {
			_, err := module.EnsureIndex(context.Background(), "players", bson.D{{Key: "", Value: 1}})
			return err
		}},
		{name: "typed nil option", call: func() error {
			_, err := module.EnsureIndex(context.Background(), "players", bson.D{{Key: "id", Value: 1}}, typedNil)
			return err
		}},
		{name: "nil setter", call: func() error {
			_, err := module.EnsureIndex(context.Background(), "players", bson.D{{Key: "id", Value: 1}}, &mongooptions.IndexOptionsBuilder{Opts: []func(*mongooptions.IndexOptions) error{nil}})
			return err
		}},
		{name: "negative ttl", call: func() error {
			_, err := module.EnsureTTLIndex(context.Background(), "players", "at", -time.Second)
			return err
		}},
		{name: "fractional ttl", call: func() error {
			_, err := module.EnsureTTLIndex(context.Background(), "players", "at", time.Second+time.Nanosecond)
			return err
		}},
		{name: "empty ttl field", call: func() error {
			_, err := module.EnsureTTLIndex(context.Background(), "players", "", time.Second)
			return err
		}},
		{name: "ttl overflow", call: func() error {
			_, err := module.EnsureTTLIndex(context.Background(), "players", "at", time.Duration(int64(math.MaxInt32)+1)*time.Second)
			return err
		}},
	}
	for _, test := range tests {
		if err := test.call(); !errs.IsCode(err, errs.CodeInvalidArgument) {
			t.Errorf("%s error = %v", test.name, err)
		}
	}
}

func TestEnsureIndexesReturnsPartialSuccess(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	runtime.createFailAt = 2
	runtime.createIndexErr = errFake
	module := startTestModule(t, runtime)
	names, err := module.EnsureIndexes(
		context.Background(),
		"players",
		mongo.IndexModel{Keys: bson.D{{Key: "id", Value: 1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "name", Value: 1}}},
	)
	if !errors.Is(err, errFake) || len(names) != 1 {
		t.Fatalf("EnsureIndexes() = %#v, %v", names, err)
	}

	emptyRuntime := newFakeRuntime()
	emptyModule := startTestModule(t, emptyRuntime)
	names, err = emptyModule.EnsureIndexes(context.Background(), "players")
	if err != nil || names == nil || len(names) != 0 || len(emptyRuntime.created) != 0 {
		t.Fatalf("empty EnsureIndexes() = %#v, %v, calls=%d", names, err, len(emptyRuntime.created))
	}
}

func TestEnsureIndexesValidatesEveryModel(t *testing.T) {
	t.Parallel()
	module := startTestModule(t, newFakeRuntime())
	names, err := module.EnsureIndexes(
		context.Background(), "players",
		mongo.IndexModel{Keys: bson.D{{Key: "id", Value: 1}}},
		mongo.IndexModel{Keys: bson.M{"unordered": 1}},
	)
	if !errs.IsCode(err, errs.CodeInvalidArgument) || len(names) != 1 {
		t.Fatalf("EnsureIndexes invalid model = %#v, %v", names, err)
	}
}

func applyIndexOptions(t *testing.T, model mongo.IndexModel) mongooptions.IndexOptions {
	t.Helper()
	var result mongooptions.IndexOptions
	for _, setter := range model.Options.List() {
		if err := setter(&result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func startTestModule(t *testing.T, runtime *fakeRuntime) *Module {
	t.Helper()
	module := configuredTestModule(runtime)
	if err := module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.OnStop(context.Background()) })
	return module
}
