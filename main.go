package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Config struct {
	URI string `json:"uri"`
	DB  string `json:"db"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "connect":
		connectCmd(os.Args[2:])
	case "list":
		listCmd(os.Args[2:])
	case "backup":
		backupCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`mongobak - MongoDB JSON backup tool

Commands:
  connect   Test connection and save config locally
  list      List databases and collections
  backup    Backup collections as JSON (Extended JSON)

Global config file:
  ~/.config/mongobak/config.json (Linux)
  %APPDATA%\mongobak\config.json (Windows)

connect:
  mongobak connect --uri "mongodb://localhost:27017" --db mydb

list:
  mongobak list
  mongobak list --db otherdb

backup:
  mongobak backup --output ./backups
  mongobak backup --exclude users,logs --output ./backups
  mongobak backup --output ./mydb.jsonl  (single file, all collections merged)

Flags (backup):
  --exclude name1,name2   Exclude collections by name
  --output  path          Directory OR file (.jsonl)
`)
}

func connectCmd(args []string) {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	uri := fs.String("uri", "", "MongoDB URI (e.g. mongodb://user:pass@host:27017)")
	db := fs.String("db", "", "Default database name")
	timeout := fs.Duration("timeout", 5*time.Second, "Connection timeout")
	_ = fs.Parse(args)

	if *uri == "" || *db == "" {
		fatal(errors.New("connect requires --uri and --db"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(*uri))
	if err != nil {
		fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	if err := client.Ping(ctx, nil); err != nil {
		fatal(err)
	}

	cfg := Config{URI: *uri, DB: *db}
	if err := saveConfig(cfg); err != nil {
		fatal(err)
	}

	fmt.Println("OK: connected and config saved.")
}

func listCmd(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dbOverride := fs.String("db", "", "Database to list collections from (optional)")
	timeout := fs.Duration("timeout", 10*time.Second, "Operation timeout")
	_ = fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		fatal(err)
	}

	dbName := cfg.DB
	if *dbOverride != "" {
		dbName = *dbOverride
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.URI))
	if err != nil {
		fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	dbs, err := client.ListDatabaseNames(ctx, bson.M{})
	if err != nil {
		fatal(err)
	}

	fmt.Println("Databases:")
	for _, d := range dbs {
		fmt.Printf(" - %s\n", d)
	}

	fmt.Printf("\nCollections in %q:\n", dbName)
	colls, err := client.Database(dbName).ListCollectionNames(ctx, bson.M{})
	if err != nil {
		fatal(err)
	}
	for _, c := range colls {
		fmt.Printf(" - %s\n", c)
	}
}

func backupCmd(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	exclude := fs.String("exclude", "", "Comma-separated collection names to exclude")
	output := fs.String("output", "", "Output directory OR file (.jsonl)")
	dbOverride := fs.String("db", "", "Database name override (optional)")
	timeout := fs.Duration("timeout", 0, "Operation timeout (0 = no timeout)")
	batchSize := fs.Int("batch", 500, "Cursor batch size")
	pretty := fs.Bool("pretty", false, "Pretty JSON (bigger files)")
	_ = fs.Parse(args)

	if *output == "" {
		fatal(errors.New("backup requires --output"))
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal(err)
	}

	dbName := cfg.DB
	if *dbOverride != "" {
		dbName = *dbOverride
	}

	exSet := map[string]bool{}
	for _, n := range splitCSV(*exclude) {
		exSet[n] = true
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if *timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), *timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.URI))
	if err != nil {
		fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	db := client.Database(dbName)
	colls, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		fatal(err)
	}

	isDir := isProbablyDir(*output)
	if isDir {
		if err := os.MkdirAll(*output, 0o755); err != nil {
			fatal(err)
		}
		fmt.Printf("Writing one file per collection into: %s\n", *output)
	} else {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			fatal(err)
		}
		fmt.Printf("Writing merged output into: %s\n", *output)
	}

	var mergedWriter *bufio.Writer
	var mergedFile *os.File
	if !isDir {
		f, err := os.Create(*output)
		if err != nil {
			fatal(err)
		}
		defer func() { _ = f.Close() }()
		mergedFile = f
		mergedWriter = bufio.NewWriterSize(f, 1<<20)
		defer func() { _ = mergedWriter.Flush() }()
		_ = mergedFile
	}

	for _, collName := range colls {
		if exSet[collName] {
			fmt.Printf("Skipping excluded collection: %s\n", collName)
			continue
		}

		coll := db.Collection(collName)
		findOpts := options.Find().SetBatchSize(int32(*batchSize))

		cur, err := coll.Find(ctx, bson.M{}, findOpts)
		if err != nil {
			fatal(fmt.Errorf("find %s: %w", collName, err))
		}

		var w io.Writer
		var file *os.File
		var bw *bufio.Writer

		if isDir {
			path := filepath.Join(*output, fmt.Sprintf("%s.%s.jsonl", dbName, collName))
			f, err := os.Create(path)
			if err != nil {
				_ = cur.Close(ctx)
				fatal(err)
			}
			file = f
			bw = bufio.NewWriterSize(f, 1<<20)
			w = bw
			fmt.Printf("Backing up %s -> %s\n", collName, path)
		} else {
			// merged output
			w = mergedWriter
			fmt.Printf("Backing up %s -> (merged)\n", collName)
		}

		count := 0
		for cur.Next(ctx) {
			var doc bson.M
			if err := cur.Decode(&doc); err != nil {
				_ = cur.Close(ctx)
				if isDir {
					_ = bw.Flush()
					_ = file.Close()
				}
				fatal(fmt.Errorf("decode %s: %w", collName, err))
			}

			// Add metadata when merged (optional but handy)
			if !isDir {
				doc["_meta"] = bson.M{"db": dbName, "collection": collName}
			}

			extJSON, err := bson.MarshalExtJSON(doc, *pretty, false)
			if err != nil {
				_ = cur.Close(ctx)
				if isDir {
					_ = bw.Flush()
					_ = file.Close()
				}
				fatal(fmt.Errorf("marshal %s: %w", collName, err))
			}

			if _, err := w.Write(extJSON); err != nil {
				_ = cur.Close(ctx)
				if isDir {
					_ = bw.Flush()
					_ = file.Close()
				}
				fatal(err)
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				_ = cur.Close(ctx)
				if isDir {
					_ = bw.Flush()
					_ = file.Close()
				}
				fatal(err)
			}
			count++
		}

		if err := cur.Err(); err != nil {
			_ = cur.Close(ctx)
			if isDir {
				_ = bw.Flush()
				_ = file.Close()
			}
			fatal(fmt.Errorf("cursor %s: %w", collName, err))
		}
		_ = cur.Close(ctx)

		if isDir {
			_ = bw.Flush()
			_ = file.Close()
		}

		fmt.Printf("Done %s (%d docs)\n", collName, count)
	}

	fmt.Println("Backup complete.")
}

// ---------- config helpers ----------

func saveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w (run: mongobak connect ...)", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.URI == "" || cfg.DB == "" {
		return Config{}, errors.New("config invalid (missing uri/db); re-run: mongobak connect ...")
	}
	return cfg, nil
}

func configPath() (string, error) {
	// Cross-platform: use os.UserConfigDir
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mongobak", "config.json"), nil
}

// ---------- misc helpers ----------

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isProbablyDir(path string) bool {
	// If exists and is dir => dir
	if st, err := os.Stat(path); err == nil {
		return st.IsDir()
	}
	// If ends with separator => dir
	if strings.HasSuffix(path, string(os.PathSeparator)) {
		return true
	}
	// If has .json or .jsonl extension => file, otherwise treat as dir
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" || ext == ".jsonl" {
		return false
	}
	return true
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
