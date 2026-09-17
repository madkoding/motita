package main

import "runtime"

// Envoltorios de runtime para no importar runtime en varios sitios.

func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }
