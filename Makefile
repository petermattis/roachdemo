.PHONY: all
all: bindata
	go install

.PHONY: bindata
bindata:
	go-bindata -ignore=.gitignore assets/...
