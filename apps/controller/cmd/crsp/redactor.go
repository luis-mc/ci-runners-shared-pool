package main

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"

// secretRedactor is the one redactor in front of runner log lines (runners R5.11).
var secretRedactor = redact.New()
