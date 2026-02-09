package frontend

import "embed"

//go:embed index.html static/*
var Assets embed.FS
