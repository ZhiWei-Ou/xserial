package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Mark struct {
	At   time.Time `json:"at"`
	Note string    `json:"note"`
}

// SaveMark keeps the source recording unchanged, appending notes to its sidecar.
// Callers serialize edits within one UI. A failed save leaves the prior file intact.
func SaveMark(path string, mark Mark) error {
	var marks []Mark
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &marks); err != nil {
			return fmt.Errorf("decode existing marks: %w", err)
		}
	}
	if len(marks) >= 10000 {
		return errors.New("mark limit is 10000")
	}
	marks = append(marks, mark)
	data, err = json.MarshalIndent(marks, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".marks-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func LoadMarks(path string) ([]Mark, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marks []Mark
	if err := json.Unmarshal(data, &marks); err != nil {
		return nil, err
	}
	return marks, nil
}
