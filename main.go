package main

import "github.com/nemaniabhiram/cider/server"

func main() {
	server := server.NewServer(":8080")
	server.Start()
}