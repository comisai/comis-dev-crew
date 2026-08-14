package livecampaign

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func (realResourceReader) RegularFileBytes(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("path is not one regular file")
	}
	return info.Size(), nil
}

func (realResourceReader) TreeTotals(root string) (int, int64, error) {
	files := 0
	var bytes int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("resource tree contains an unexpected symbolic link")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > int64(^uint64(0)>>1)-bytes {
			return errors.New("resource tree byte total exceeds local bounds")
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func (realResourceReader) DirectoryCount(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return 0, errors.New("worktree root contains an unexpected non-directory entry")
		}
		count++
	}
	return count, nil
}

func (realResourceReader) OpenFDCount(pid uint64) (int, error) {
	entries, err := os.ReadDir(filepath.Join("/proc", strconv.FormatUint(pid, 10), "fd"))
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func (realResourceReader) ProcessRSSBytes(pid uint64) (int64, error) {
	file, err := os.Open(filepath.Join("/proc", strconv.FormatUint(pid, 10), "status"))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
			kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || kilobytes <= 0 || kilobytes > int64(^uint64(0)>>1)/1024 {
				return 0, errors.New("process RSS is invalid")
			}
			return kilobytes * 1024, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("process RSS is unavailable")
}

func (realResourceReader) JailProcessCount(rootPID uint64) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	parents := make(map[uint64]uint64)
	names := make(map[uint64]string)
	for _, entry := range entries {
		pid, err := strconv.ParseUint(entry.Name(), 10, 64)
		if err != nil || !entry.IsDir() {
			continue
		}
		parent, name, err := readProcessIdentity(pid)
		if err != nil {
			continue
		}
		parents[pid] = parent
		names[pid] = name
	}
	count := 0
	for pid, name := range names {
		if (name == "bwrap" || name == "bubblewrap") && processDescendsFrom(pid, rootPID, parents) {
			count++
		}
	}
	return count, nil
}

func readProcessIdentity(pid uint64) (uint64, string, error) {
	root := filepath.Join("/proc", strconv.FormatUint(pid, 10))
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return 0, "", err
	}
	closeIndex := strings.LastIndex(string(stat), ") ")
	if closeIndex < 0 {
		return 0, "", errors.New("process stat is malformed")
	}
	fields := strings.Fields(string(stat)[closeIndex+2:])
	if len(fields) < 2 {
		return 0, "", errors.New("process stat is incomplete")
	}
	parent, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, "", errors.New("process parent is malformed")
	}
	name, err := os.ReadFile(filepath.Join(root, "comm"))
	if err != nil {
		return 0, "", err
	}
	return parent, strings.TrimSpace(string(name)), nil
}

func processDescendsFrom(pid, rootPID uint64, parents map[uint64]uint64) bool {
	for steps := 0; steps < len(parents); steps++ {
		parent, exists := parents[pid]
		if !exists || parent == 0 || parent == pid {
			return false
		}
		if parent == rootPID {
			return true
		}
		pid = parent
	}
	return false
}

func (realResourceReader) ActiveTerminalCount(path string) (int, error) {
	return sqliteCount(path, "SELECT COUNT(*) FROM task_terminal_bindings WHERE latest_transition NOT IN ('exited','lost','released')")
}

func (realResourceReader) PendingDevCrewDeliveryCount(path string) (int, error) {
	return sqliteCount(path, "SELECT COUNT(*) FROM comis_report_outbox WHERE delivered_at IS NULL")
}

func (realResourceReader) UnsettledComisDeliveryCount(path string) (int, error) {
	return sqliteCount(path, "SELECT COUNT(*) FROM delivery_queue WHERE status IN ('pending','in_flight','failed')")
}

func sqliteCount(path, query string) (int, error) {
	location := url.URL{Scheme: "file", Path: path}
	parameters := location.Query()
	parameters.Set("mode", "ro")
	location.RawQuery = parameters.Encode()
	database, err := sql.Open("sqlite", location.String())
	if err != nil {
		return 0, err
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	var count int64
	if err := database.QueryRow(query).Scan(&count); err != nil {
		return 0, fmt.Errorf("read SQLite resource count: %w", err)
	}
	if count < 0 || uint64(count) > uint64(^uint(0)>>1) {
		return 0, errors.New("SQLite resource count exceeds local bounds")
	}
	return int(count), nil
}
