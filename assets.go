package main

import "embed"

//go:embed frontend/dist
var loadingAssets embed.FS
