package main

import (
	"flag"
	"fmt"
	"os"
	"text/template"
)

func main() {

	type Service struct {
		Name string
		Port int
	}

    name := flag.String("name", "", "service name")
	port := flag.Int("port", 8080, "port")
	flag.Parse()

	if *name == "" {
		fmt.Println(os.Stderr, "--name is required --port\n")
		os.Exit(1)
	}

	fmt.Printf("name=%s, port=%d\n", *name, *port)
	
	tmpl := "name : {{.Name}}\nport : {{.Port}}\n"
	 t, err := template.New("service").Parse(tmpl)
	 if err != nil {
		fmt.Println(err)
		os.Exit(1)
	 }
	f, err := os.Create(*name + ".yaml")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	 }
	defer f.Close()

	svc := Service{Name: *name, Port: *port}
	if err := t.Execute(f, svc); err != nil {
		fmt.Println(err)
		os.Exit(1)
	 }
}