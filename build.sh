

TAG="$(git tag -l --sort=-v:refname | head -1)"
if [ -n "$TAG" ]; then
  BASE="$(printf "%s" "$TAG" | sed 's/^v//')"
  DESC="$(git describe --long --tags | sed 's/\([^-].*\)-\([0-9]*\)-\(g.*\)/r\2.\3/g' | tr -d '-')"
  VERS="${BASE}.${DESC}"
else
  VERS="v0.0.0"
fi
DAT=$(date +%Y%m%dT%H%M%S)

go build -o gotop \
	-ldflags "-X main.Version=v${VERS} -X main.BuildDate=${DAT}" \
	./cmd/gotop
