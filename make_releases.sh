#! /bin/bash -e

OUTDIR=releases

rm -rf $OUTDIR
mkdir -p $OUTDIR


for os in darwin linux windows; do
  for arch in amd64 386; do
    if [ "$os" = "windows" ] && [ "$arch" == "arm" ]; then
      continue
    fi

    echo "Building for $os / $arch"
    env CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -o $OUTDIR/pipe2mattermost-$os-$arch .
  done
done
