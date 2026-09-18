package main

import "goteway/app"

func main() {
	s := app.NewServer()

	if err := s.Run(); err != nil {
		panic(err)
	}
}
