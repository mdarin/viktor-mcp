package tools

type ConfigGetIn struct {
	Key string `json:"key"`
}

type ConfigSetIn struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ConfigListIn struct{}

type ConfigUpdateIn struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ConfigDeleteIn struct {
	Key string `json:"key"`
}

func registerConfig(r *Registry) {
	add[ConfigGetIn](r, "config_get", "Read a config value", handleConfigGet(r))
	add[ConfigSetIn](r, "config_set", "Write a config value", handleConfigSet(r))
	add[ConfigListIn](r, "config_list", "List all config keys", handleConfigList(r))
	add[ConfigUpdateIn](r, "config_update", "Update an existing config value", handleConfigUpdate(r))
	add[ConfigDeleteIn](r, "config_delete", "Delete a config key", handleConfigDelete(r))
}
