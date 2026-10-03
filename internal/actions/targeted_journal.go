package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// EditProgress is private recovery material, not model-visible tool output.
// Persist the whole snapshot durably before returning nil from the journal.
// Records describe last-confirmed effects and write intents, NOT current disk
// state or authorization to replay/recover on resume. A nonterminal saved status
// must be presented as interrupted and reconciled manually, without any writes.
type EditProgress struct {
	Status              string                `json:"status"`
	Root                string                `json:"root"`
	Paths               []string              `json:"paths"`
	Directories         []string              `json:"directories"`
	Applied             []string              `json:"applied"`
	Restored            []string              `json:"restored"`
	Unresolved          []string              `json:"unresolved"`
	LeftoverDirectories []string              `json:"leftover_directories"`
	RetainedFiles       []string              `json:"retained_files"`
	Detail              string                `json:"detail,omitempty"`
	Files               []EditFileRecord      `json:"files"`
	DirectoryRecords    []EditDirectoryRecord `json:"directory_records"`
	Pending             EditProgressStep      `json:"pending"`
}

// Artifact paths are repository-relative candidates selected before any effect.
// Present flags are last-confirmed ownership, not a guarantee that an intent did
// not execute after a crash. Cleanup lists every reserved cleanup destination.
type EditFileRecord struct {
	Path              string           `json:"path"`
	OldText           string           `json:"old_text"`
	NewText           string           `json:"new_text"`
	Stage             string           `json:"stage"`
	Backup            string           `json:"backup"`
	Quarantine        string           `json:"quarantine"`
	Original          EditFileIdentity `json:"original"`
	Staged            EditFileIdentity `json:"staged"`
	BackupIdentity    EditFileIdentity `json:"backup_identity"`
	StagePresent      bool             `json:"stage_present"`
	BackupPresent     bool             `json:"backup_present"`
	QuarantinePresent bool             `json:"quarantine_present"`
	Published         bool             `json:"published"`
	Restored          bool             `json:"restored"`
	Unresolved        bool             `json:"unresolved"`
	Cleanup           []string         `json:"cleanup"`
}

type EditDirectoryRecord struct {
	Path       string           `json:"path"`
	Stage      string           `json:"stage"`
	Quarantine string           `json:"quarantine"`
	Current    string           `json:"current"`
	Original   EditFileIdentity `json:"original"`
	Identity   EditFileIdentity `json:"identity"`
	Owned      bool             `json:"owned"`
}

// Mode contains Unix mode/type bits; absent/unknown metadata has Exists=false.
// Device/inode are meaningful only on the original filesystem. They are evidence
// for inspection, never permission to write or proof that content is unchanged.
type EditFileIdentity struct {
	Exists          bool   `json:"exists"`
	Device          uint64 `json:"device"`
	Inode           uint64 `json:"inode"`
	Mode            uint32 `json:"mode"`
	Size            int64  `json:"size"`
	ModTimeUnixNano int64  `json:"mod_time_unix_nano"`
}

// Pending identifies the next mutation in a write-ahead record. A crash can
// happen either side of that mutation, regardless of the saved Present flags.
type EditProgressStep struct {
	Action string `json:"action,omitempty"`
	Path   string `json:"path,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// SetJournal installs (or clears with nil) private synchronous persistence before
// Apply is used. Callbacks run with the proposal locked and must not reenter
// Apply/SetJournal. Each callback receives detached slices/value records that it
// may retain; mutating a snapshot cannot change the approved write plan.
func (p *EditProposal) SetJournal(journal func(EditProgress) error) error {
	if p == nil || p.state == nil {
		return errors.New("invalid edit proposal")
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if p.state.used {
		return errors.New("cannot change the journal after an edit proposal is used")
	}
	p.state.journal = journal
	return nil
}

func (a *targetedApply) reserveNames() error {
	for _, plan := range a.proposal.dirs {
		d := a.dirs[plan.path]
		if plan.before == nil {
			for _, dest := range []*string{&d.stagePlan, &d.cleanupPlan} {
				name, err := targetedTempName()
				if err != nil {
					return err
				}
				*dest = name
			}
		}
	}
	for _, f := range a.files {
		// Reserving recovery/cleanup names in the FIRST durable record keeps
		// them discoverable even if persistence fails during bounded recovery.
		for _, dest := range []*string{&f.stagePlan, &f.quarantinePlan, &f.stageCleanup, &f.quarantineCleanup} {
			name, err := targetedTempName()
			if err != nil {
				return err
			}
			*dest = name
		}
		if f.plan.before != nil {
			for _, dest := range []*string{&f.backupPlan, &f.backupCleanup} {
				name, err := targetedTempName()
				if err != nil {
					return err
				}
				*dest = name
			}
		}
	}
	return nil
}

func (a *targetedApply) checkpoint(status string, pending EditProgressStep, detail string) error {
	a.phase = status
	if a.proposal.journal == nil {
		return nil
	}
	if err := a.proposal.journal(a.progress(status, pending, detail)); err != nil {
		a.journalFailed = true
		return &targetedJournalError{status: status, cause: err}
	}
	return nil
}

// A callback's diagnostics may contain SQL parameters/private original text.
// Preserve the cause for private errors.Is/As diagnostics, but do not copy its
// message into the model-visible Apply summary or ordinary returned error text.
type targetedJournalError struct {
	status string
	cause  error
}

func (e *targetedJournalError) Error() string {
	return fmt.Sprintf("persist edit progress (%s): private journal write failed", e.status)
}

func (e *targetedJournalError) Unwrap() error { return e.cause }

// Recovery/abort cleanup must proceed safely even when the private journal is
// unavailable. The first record already contains all reserved artifact names.
func (a *targetedApply) checkpointBestEffort(pending EditProgressStep) {
	if err := a.checkpoint(a.phase, pending, "Bounded recovery/cleanup; no replay on resume."); err != nil {
		a.journalFailures = append(a.journalFailures, err)
	}
}

func (a *targetedApply) takeJournalFailures() error {
	err := errors.Join(a.journalFailures...)
	a.journalFailures = nil
	return err
}

func targetedArtifact(parts []string, name string) string {
	if name == "" {
		return ""
	}
	return filepath.Join(targetedParentPath(parts), name)
}

func targetedCandidate(actual, planned string) string {
	if actual != "" {
		return actual
	}
	return planned
}

func targetedFileMetadata(info os.FileInfo) EditFileIdentity {
	if info == nil {
		return EditFileIdentity{}
	}
	metadata := EditFileIdentity{Exists: true, Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		metadata.Device, metadata.Inode, metadata.Mode = uint64(stat.Dev), uint64(stat.Ino), uint32(stat.Mode)
	}
	return metadata
}

func (a *targetedApply) progress(status string, pending EditProgressStep, detail string) EditProgress {
	p := EditProgress{Status: status, Root: a.proposal.root, Pending: pending, Detail: detail,
		Paths: []string{}, Directories: []string{}, Applied: []string{}, Restored: []string{}, Unresolved: []string{},
		LeftoverDirectories: []string{}, RetainedFiles: []string{}, Files: []EditFileRecord{}, DirectoryRecords: []EditDirectoryRecord{}}
	for _, f := range a.files {
		p.Paths = append(p.Paths, f.plan.path)
		record := EditFileRecord{Path: f.plan.path, OldText: f.plan.old, NewText: f.plan.next,
			Stage:      targetedArtifact(f.plan.parts, targetedCandidate(f.stage, f.stagePlan)),
			Backup:     targetedArtifact(f.plan.parts, targetedCandidate(f.backup, f.backupPlan)),
			Quarantine: targetedArtifact(f.plan.parts, targetedCandidate(f.quarantine, f.quarantinePlan)),
			Original:   targetedFileMetadata(f.plan.before), Staged: targetedFileMetadata(f.stageInfo),
			StagePresent: f.stage != "", BackupPresent: f.backup != "", QuarantinePresent: f.quarantine != "",
			Published: f.published, Restored: f.restored, Unresolved: f.unresolved, Cleanup: []string{}}
		if f.backupID != (targetedIdentity{}) {
			record.BackupIdentity = EditFileIdentity{Exists: true, Device: f.backupID.device, Inode: f.backupID.inode, Mode: f.backupID.mode}
		}
		for _, name := range []string{f.stageCleanup, f.backupCleanup, f.quarantineCleanup} {
			if name != "" {
				record.Cleanup = append(record.Cleanup, targetedArtifact(f.plan.parts, name))
			}
		}
		p.Files = append(p.Files, record)
		if f.published {
			p.Applied = append(p.Applied, f.plan.path)
		}
		if f.restored {
			p.Restored = append(p.Restored, f.plan.path)
		}
		if f.unresolved || status == "interrupted" && f.published && !f.restored {
			p.Unresolved = append(p.Unresolved, f.plan.path)
		}
		for _, name := range []string{f.stage, f.backup, f.quarantine} {
			if name != "" {
				p.RetainedFiles = append(p.RetainedFiles, targetedArtifact(f.plan.parts, name))
			}
		}
	}
	for _, plan := range a.proposal.dirs {
		d := a.dirs[plan.path]
		record := EditDirectoryRecord{Path: plan.path, Original: targetedFileMetadata(plan.before), Identity: targetedFileMetadata(d.info),
			Stage: targetedArtifact(plan.parts, d.stagePlan), Quarantine: targetedArtifact(plan.parts, d.cleanupPlan), Owned: d.owned}
		if d.owned {
			record.Current = targetedArtifact(plan.parts, d.name)
			if status != "completed" {
				p.LeftoverDirectories = append(p.LeftoverDirectories, record.Current)
			}
		}
		p.DirectoryRecords = append(p.DirectoryRecords, record)
		if plan.before == nil {
			p.Directories = append(p.Directories, plan.path)
		}
	}
	return p
}

func (a *targetedApply) finalJournalStatus(err error) string {
	if err == nil && a.report.Status == "complete" {
		return "completed"
	}
	if a.report.Status == "partial" {
		return "interrupted"
	}
	return "failed"
}
