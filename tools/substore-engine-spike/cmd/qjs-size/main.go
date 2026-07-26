package main

import "github.com/fastschema/qjs"

func main() {
	rt, err := qjs.New()
	if err != nil {
		panic(err)
	}
	rt.Close()
}
