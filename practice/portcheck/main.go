package main

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// A minimal 'main' package in Go must include a main() function.
// This is the entry point of the executable program.
// The file usually starts with 'package main', followed by necessary imports and the main function.

type Target struct {
	Destination string
	OK bool
	Err error
}


var destinations = []string{
	"google.com:443",
	"github.com:443",
	"github.com:80",
	"cloudflare.com:443",
	"amazon.com:443",
	"8.8.8.8:53",
	"golang.org:443",
	"stackoverflow.com:443",
	"172.16.90.92:80",
} 

func checkPort(destination string, wg *sync.WaitGroup, targets chan<- Target) {
	defer wg.Done()
	conn, err := net.DialTimeout("tcp", destination, 2*time.Second)
	if err != nil {
		targets <- Target{Destination: destination, OK: false, Err: err}
		return
	}
	conn.Close()
	targets <- Target{Destination: destination, OK: true, Err: nil}
}


func main() {
	var wg sync.WaitGroup
	
    targets := make(chan Target, len(destinations))
    for _, destination := range destinations {
        wg.Add(1)
   
		go checkPort(destination, &wg, targets)
	}
	go func() {
		wg.Wait()
		close(targets)
	}()

    var countOK, countErr int

	for result := range targets {
		if result.OK {
			countOK++
			fmt.Println("Destination: ", result.Destination, "OK: ", result.OK)
		} else {
			countErr++
			fmt.Println("Destination: ", result.Destination, "OK: ", result.OK, "Err: ", result.Err)
		}

		
	}

	fmt.Printf("OK: %d, Err: %d, Total: %d\n", countOK, countErr, len(destinations))
}