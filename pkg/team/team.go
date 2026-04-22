// Package team composes Agents into higher-level coordinators that expose
// the same actor interface as a single Agent. Every pattern (Pipeline,
// Broadcast, Router, Hierarchy, Debate) spawns a coordinator actor that
// accepts agent.Prompt and replies with agent.Response (or agent.Error),
// so teams nest cleanly inside other teams.
//
// The fact that each Agent owns its own llm.Provider is the point: a
// single team can mix Ollama, Anthropic, OpenAI, and Mistral stages
// without any of the coordinator code knowing about providers at all.
package team
