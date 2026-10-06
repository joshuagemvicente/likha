package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const chatGPTPlanTitle = "You're using your ChatGPT plan"
const chatGPTPlanBody = "Likha uses your shared ChatGPT plan allowance and limits. Credits are used only if you opt in in ChatGPT Settings; API-key billing is separate."
const chatGPTUsageURL = "https://chatgpt.com/settings/usage"

// Notices are UI preferences, not OAuth credentials. Keep them separate from
// provider storage and acknowledge once per issued registration, not per email.
func readChatGPTPlanNotices(stateDir string) (map[string]bool, error) {
	notices := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(stateDir, "oauth-notices.json"))
	if os.IsNotExist(err) {
		return notices, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &notices)
	return notices, err
}

func chatGPTPlanAcknowledged(stateDir, clientID string) (bool, error) {
	notices, err := readChatGPTPlanNotices(stateDir)
	return notices[clientID], err
}

func acknowledgeChatGPTPlan(stateDir, clientID string) error {
	notices, err := readChatGPTPlanNotices(stateDir)
	if err != nil {
		return err
	}
	notices[clientID] = true
	data, err := json.Marshal(notices)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(stateDir, ".oauth-notices-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(stateDir, "oauth-notices.json"))
}
