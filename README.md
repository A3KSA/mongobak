[![Build & Release](https://github.com/A3KSA/mongobak/actions/workflows/go.yml/badge.svg)](https://github.com/A3KSA/mongobak/actions/workflows/go.yml)


go mod tidy
go build -o mongobak .

./mongobak connect --uri "mongodb://localhost:27017" --db missaticus
./mongobak list
./mongobak backup --exclude logs,tmp --output ./backups
./mongobak backup --output ./all.jsonl
