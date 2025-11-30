# CONFIGS
## Basic Config
```json
{
  "models": [
    {
      "name": "gpt-5.1",
      "provider": "openai",
      "apiKey": "YOUR API KEY",
      "active": true
    }
  ],
  "contextRetentionTurns": 5,
  "logLevel": "info",
  "toolCallMode": "auto"
}
```

## MCP Servers
```json
{
  "mcpServers": {
    "context7": {
      "enabled": true,
      "command": "npx",
      "args": [
        "-y",
        "@upstash/context7-mcp"
      ]
    },
    "filesystem": {
      "enabled": true,
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-filesystem",
        "/home/gamzabox/workspace",
        "/home/gamzabox/temp"
      ]
    }
  }
}
```

# WORKFLOWS
## Summary of Methods for Retrieving YouTube Transcripts in TypeScript
Find a module in TypeScript that can extract YouTube transcripts, and summarize its features and usage.

## Save the Summary to a File
Save the summarized content to the following path:
/home/myhome/temp/how_to_get_youtube_script.md
