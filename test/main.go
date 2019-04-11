package main

import (
	"flag"
	"log"
	"os"

	"github.com/gyf1214/h2quicsim"
)

var (
	path = flag.String("path", "", "path")
)

func main() {
	flag.Parse()
	fin, err := os.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer fin.Close()
	objs, err := h2quicsim.LoadObjects(fin)
	if err != nil {
		log.Fatal(err)
	}

	entMap := make(map[int][]*h2quicsim.Entry)
	for idx, ent := range objs {
		entMap[ent.Connection] = append(entMap[ent.Connection], &objs[idx])
	}

	for _, ents := range entMap {
		for _, e := range ents {
			log.Println(e.Stream, e.Dependency)
		}
		log.Println()
	}
}
