package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/underwoo/elzar/internal/slurm"
)

func TestLoadFileAndCommandLineOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "refresh_interval = \"30s\"\nrecent_window = \"2h\"\ndefault_scope = \"all\"\ncolumns = [\"job_id\"]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RefreshInterval != 30*time.Second || settings.RecentWindow != 2*time.Hour || settings.DefaultScope != slurm.ScopeAll {
		t.Fatalf("loaded settings = %+v", settings)
	}
	settings, err = Override(settings, "5s", "45m", false)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RefreshInterval != 5*time.Second || settings.RecentWindow != 45*time.Minute || settings.DefaultScope != slurm.ScopeAll {
		t.Fatalf("overridden settings = %+v", settings)
	}
	settings, err = Override(settings, "", "", true)
	if err != nil || settings.DefaultScope != slurm.ScopeAll {
		t.Fatalf("--all settings = %+v, err %v", settings, err)
	}
}

func TestLoadFileReadsColumnTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("refresh_interval = \"22s\"\n[columns]\nqueue = [\"name\", \"job_id\"]\nrecent = [\"end\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RefreshInterval != 22*time.Second || len(settings.QueueColumns) != 2 || settings.QueueColumns[0] != "name" || len(settings.RecentColumns) != 1 || settings.RecentColumns[0] != "end" {
		t.Fatalf("loaded settings = %+v", settings)
	}
}

func TestLoadFileReadsTabPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[keys]\ntab_prefix = \"alt+x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.TabPrefix != "alt+x" {
		t.Fatalf("tab prefix = %q, want alt+x", settings.TabPrefix)
	}
}

func TestLoadFileRejectsInvalidTabPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[keys]\ntab_prefix = \"not a key\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "keys.tab_prefix") {
		t.Fatalf("LoadFile error = %v, want invalid tab_prefix error", err)
	}
}

func TestSaveColumnsPreservesBytesOutsideTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	before := "# keep this comment\nrefresh_interval = \"30s\"\n\n[columns] # old selection\nqueue = [\"job_id\"]\n# old table comment\nrecent = [\"end\"]\n\n[other]\nvalue = \"keep\" # keep inline comment\n"
	if err := os.WriteFile(path, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SaveColumns(path, []string{"name", "job_id"}, []string{"state"}); err != nil {
		t.Fatal(err)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after := string(afterBytes)
	want := "# keep this comment\nrefresh_interval = \"30s\"\n\n[columns]\nqueue = [\"name\", \"job_id\"]\nrecent = [\"state\"]\n\n[other]\nvalue = \"keep\" # keep inline comment\n"
	if after != want {
		t.Fatalf("saved config:\n%s\nwant:\n%s", after, want)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config permissions = %v, err=%v", info, err)
	}
}

func TestSaveColumnsCreatesMissingFileAndDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := SaveColumns(path, []string{"job_id"}, []string{"end"}); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.QueueColumns) != 1 || settings.QueueColumns[0] != "job_id" || len(settings.RecentColumns) != 1 || settings.RecentColumns[0] != "end" {
		t.Fatalf("saved settings = %+v", settings)
	}
}

func TestSaveColumnsDoesNotOverwriteBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	bad := "refresh_interval = [not valid\n"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveColumns(path, []string{"job_id"}, nil); err == nil {
		t.Fatal("malformed config was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != bad {
		t.Fatalf("bad config changed to %q", after)
	}
}

func TestLoadFileMissingUsesDefaults(t *testing.T) {
	settings, err := LoadFile(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.RefreshInterval != Defaults().RefreshInterval || settings.RecentWindow != Defaults().RecentWindow || settings.DefaultScope != Defaults().DefaultScope || len(settings.QueueColumns) != 0 || len(settings.RecentColumns) != 0 {
		t.Fatalf("defaults = %+v, want %+v", settings, Defaults())
	}
}

func TestInvalidDurationIsRejected(t *testing.T) {
	if _, err := Override(Defaults(), "0s", "", false); err == nil {
		t.Fatal("zero refresh interval was accepted")
	}
}

func TestAccountSettingsAndColumnPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	source := "# keep this\n[accounts]\nclusters = \"c1,c2\"\nrefresh_interval = \"7m\"\n\n[columns]\naccounts = [\"account\", \"you\"]\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFile(path)
	if err != nil || settings.AccountClusters != "c1,c2" || settings.AccountRefreshInterval != 7*time.Minute || len(settings.AccountColumns) != 2 {
		t.Fatalf("%+v %v", settings, err)
	}
	if err := SaveColumns(path, []string{"job_id"}, []string{"name"}); err != nil {
		t.Fatal(err)
	}
	settings, err = LoadFile(path)
	if err != nil || len(settings.AccountColumns) != 2 {
		t.Fatalf("lost accounts columns: %+v %v", settings, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "# keep this\n[accounts]\nclusters = \"c1,c2\"\nrefresh_interval = \"7m\"\n") {
		t.Fatal("accounts table changed")
	}
	for _, value := range []string{"refresh_interval = \"0s\"", "refresh_interval = 1", "clusters = [\"c1\"]"} {
		if err := os.WriteFile(path, []byte("[accounts]\n"+value), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestLegacyConfigFallbackSaveAndPrecedence(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	legacyPath := filepath.Join(base, "slurmtui", "config.toml")
	newPath := filepath.Join(base, "elzar", "config.toml")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyContent := "# legacy header\nrefresh_interval = \"42s\"\n\n[columns]\nqueue = [\"job_id\", \"name\"]\nrecent = [\"end\"]\n\n[other]\nkeep = \"legacy\"\n"
	if err := os.WriteFile(legacyPath, []byte(legacyContent), 0o640); err != nil {
		t.Fatal(err)
	}

	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	wantNotice := "using legacy config " + legacyPath + "; move it to " + filepath.Dir(newPath) + "/"
	if settings.Path != newPath || settings.LegacyPath != legacyPath || settings.Notice != wantNotice || settings.RefreshInterval != 42*time.Second {
		t.Fatalf("legacy load = %+v, want notice %q", settings, wantNotice)
	}

	if err := SaveColumns(newPath, []string{"name"}, []string{"state"}); err != nil {
		t.Fatal(err)
	}
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyAfter) != legacyContent {
		t.Fatalf("legacy config was modified:\n%s", legacyAfter)
	}
	newAfter, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	wantNew := "# legacy header\nrefresh_interval = \"42s\"\n\n[columns]\nqueue = [\"name\"]\nrecent = [\"state\"]\n\n[other]\nkeep = \"legacy\"\n"
	if string(newAfter) != wantNew {
		t.Fatalf("new config after first save:\n%s\nwant:\n%s", newAfter, wantNew)
	}
	info, err := os.Stat(newPath)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("new config permissions = %v, err=%v", info, err)
	}

	if err := os.WriteFile(newPath, []byte("refresh_interval = \"11s\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.RefreshInterval != 11*time.Second || settings.LegacyPath != "" || settings.Notice != "" {
		t.Fatalf("new-path precedence failed: %+v", settings)
	}
}
