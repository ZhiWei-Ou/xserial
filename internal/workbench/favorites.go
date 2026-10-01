package workbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

const maxFavorites = 100

type Favorite struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

// LoadFavorites accepts a missing file as an empty collection, but reports corrupt
// existing files so that saving cannot silently overwrite the user's commands.
func LoadFavorites(path string) ([]Favorite, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read command favorites: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 2<<20+1))
	if err != nil {
		return nil, fmt.Errorf("read command favorites: %w", err)
	}
	if len(data) > 2<<20 {
		return nil, errors.New("command favorites file exceeds 2 MiB")
	}
	var favorites []Favorite
	if err := json.Unmarshal(data, &favorites); err != nil {
		return nil, fmt.Errorf("decode command favorites: %w", err)
	}
	if err := validateFavorites(favorites); err != nil {
		return nil, err
	}
	return favorites, nil
}

func validateFavorites(favorites []Favorite) error {
	if len(favorites) > maxFavorites {
		return fmt.Errorf("command favorites limit is %d", maxFavorites)
	}
	for _, favorite := range favorites {
		data, err := hexdata.Parse(favorite.Hex)
		if err != nil || len(data) == 0 || len(favorite.Hex) > maxInputChars || len([]rune(favorite.Name)) > 80 || strings.TrimSpace(favorite.Name) == "" {
			return fmt.Errorf("invalid command favorite %q", favorite.Name)
		}
	}
	return nil
}

// SaveFavorites replaces the file only after the new JSON has been fully written.
func SaveFavorites(path string, favorites []Favorite) (err error) {
	if path == "" {
		return errors.New("no favorites file configured")
	}
	if err := validateFavorites(favorites); err != nil {
		return err
	}
	data, err := json.MarshalIndent(favorites, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create favorites directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".commands-*")
	if err != nil {
		return fmt.Errorf("create favorites file: %w", err)
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write command favorites: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close command favorites: %w", err)
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace command favorites: %w", err)
	}
	return nil
}
