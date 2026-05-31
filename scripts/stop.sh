#!/bin/bash
cd "$(cd "$(dirname "$0")" && pwd)"
docker compose down
echo "✅ Stopped"
