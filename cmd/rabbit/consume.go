package main

import "goteway/app"

func main() {
	s := app.NewServer()
	s.DeclareQueues()
	s.RunConsummers()
}
