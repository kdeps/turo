// scan.go — kartographer index reading for turo --scan mode.
package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

type idxDoc struct {
	Path string
}

func scanTree(root string, maxDepth int) (string, error) {
	dbPath := filepath.Join(root, ".kdeps", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return "", fmt.Errorf("no kartographer index at %s — run 'kdeps /search index' first", dbPath)
	}
	db, err := bolt.Open(dbPath, 0o444, &bolt.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer db.Close()

	paths, err := readPaths(db)
	if err != nil {
		return "", err
	}

	absRoot, _ := filepath.Abs(root)
	rootName := filepath.Base(absRoot)
	rootNode := &tNode{name: rootName, isDir: true}
	seen := make(map[string]bool)

	for _, p := range paths {
		rel, err := filepath.Rel(absRoot, p)
		if err != nil {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > 0 && noiseDir(parts[0]) {
			continue
		}
		if len(parts) > maxDepth {
			parts = parts[:maxDepth]
		}
		insertTNode(rootNode, parts, seen)
	}

	return formatTree(rootNode), nil
}

type tNode struct {
	name     string
	isDir    bool
	children []*tNode
}

func insertTNode(parent *tNode, parts []string, seen map[string]bool) {
	if len(parts) == 0 {
		return
	}
	name := parts[0]
	isLast := len(parts) == 1

	for _, c := range parent.children {
		if c.name == name {
			if c.isDir && !isLast {
				insertTNode(c, parts[1:], seen)
			}
			return
		}
	}
	key := strings.Join(parts, "/")
	if seen[key] {
		return
	}
	seen[key] = true

	node := &tNode{name: name, isDir: !isLast}
	parent.children = append(parent.children, node)
	if !isLast {
		insertTNode(node, parts[1:], seen)
	}
}

func formatTree(n *tNode) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s/\n", n.name)
	writeTNode(&sb, n, "")
	return sb.String()
}

func writeTNode(sb *strings.Builder, n *tNode, prefix string) {
	children := n.children
	sort.Slice(children, func(i, j int) bool {
		if children[i].isDir != children[j].isDir {
			return children[i].isDir
		}
		return children[i].name < children[j].name
	})
	for i, c := range children {
		last := i == len(children)-1
		conn := "├── "
		if last {
			conn = "└── "
		}
		name := c.name
		if c.isDir {
			name += "/"
		}
		fmt.Fprintf(sb, "%s%s%s\n", prefix, conn, name)
		nextPrefix := prefix
		if last {
			nextPrefix += "    "
		} else {
			nextPrefix += "│   "
		}
		if c.isDir {
			writeTNode(sb, c, nextPrefix)
		}
	}
}

func readPaths(db *bolt.DB) ([]string, error) {
	var paths []string
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return fmt.Errorf("no docs bucket")
		}
		return b.ForEach(func(_, v []byte) error {
			var doc idxDoc
			if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&doc); err != nil {
				return nil // skip corrupt entries
			}
			if doc.Path != "" {
				paths = append(paths, doc.Path)
			}
			return nil
		})
	})
	return paths, err
}

var noiseSet = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, "dist": true, "build": true,
	".next": true, ".turbo": true, "coverage": true, "target": true,
}

func noiseDir(n string) bool { return noiseSet[n] }
