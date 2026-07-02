package main

import "github.com/mwtrigg/nugctl/cmd"

var version = "dev"

func main() {
	cmd.Execute(version)
}
