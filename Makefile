BIN := gpclient-gui

.PHONY: build run clean deps install

deps:
	go mod tidy

build: deps
	go build -o $(BIN) .

run: build
	./$(BIN)

clean:
	rm -f $(BIN)

install: build
	install -Dm755 $(BIN) /usr/local/bin/$(BIN)
	sh scripts/install-sudoers.sh
	@echo "Installed $(BIN). Run as a normal user."
