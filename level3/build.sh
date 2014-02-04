#!/bin/sh

set -e

if [ ! -f go/bin/go ]; then
  if [ ! -f go1.2.linux-amd64.tar.gz ]; then
      wget -nv https://go.googlecode.com/files/go1.2.linux-amd64.tar.gz
  fi
  tar -zxf go1.2.linux-amd64.tar.gz
fi

currdir=`pwd`
export GOROOT=$currdir/go
# export PATH=$PATH:$GOROOT/bin

rm -f bin/server
rm -f server

go/bin/go build server.go
cp server bin/server

