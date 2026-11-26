---
change: minor
task: CRSP323
---
CI only, this repository: the tools stage of the Linux base image is stored as a public ghcr.io package per architecture and input key (scripts/ci/image_key.py). Only main publishes it (base-image.yml, weekly clean rebuild); the CI image job reads it anonymously, reuses it when main built it for the same key, else builds as before.
