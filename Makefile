BIN := gpoc-gui

.PHONY: build run clean deps install desktop

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

desktop:
	install -Dm644 gpoc-gui.desktop ~/.local/share/applications/gpoc-gui.desktop
	install -Dm644 assets/vpn-green.png ~/.local/share/icons/hicolor/256x256/apps/gpoc-gui.png
	@echo "Installed desktop file and icon to ~/.local/share/"
	@echo "Run 'update-desktop-database ~/.local/share/applications/' to refresh menu"
