**이 파일은 작업용 프롬프트를 기록한 파일로 작업시 참고 하지 않는다.**

# 첫번쨰 기능 구현 프롬프트
- LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.
- REQUIREMENTS.md 에 정의된 요구사항들에 따라 AI 대화기능을 제공하는 CLI 프로그램 제작.

# config 및 session 디렉토리 위치 조정
- config.json 파일과 system_prompt.txt, 세션 파일 위치를 다음을 참고해 변경 해줘
    - config.json 파일: $HOME/.humble-ai-cli/config.json
    - system_prompt.txt 파일: $HOME/.humble-ai-cli/system_prompt.txt
    - 대화 세션 디렉토리: $HOME/.humble-ai-cli/sessions/

# 새 세션 생성 커맨트 추가
- 새 세션을 생성하고 전환하는 커맨드 추가 해줘.
- /new 커맨드 입력시 새로운 세션을 메모리에 생성
- 새 세션에서 새로운 대화를 입력하고 답변을 받으면 세션 파일을 생성 할 것.
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# MCP Server 호출 기능 추가
- MCP Server 설정은 $HOME/.humble-ai-cli/mcp-servers.json 파일의 `mcpServers` 맵으로 관리됨
  - MCP Server 설정에는 enable/disable 을 설정 할 수 있고 enable 된 MCP Server 만 initialize 하고 호출 할 수 있음
- LLM 이 필요시 MCP Server 호출을 요청 할 수 있고 humble-ai-cli 를 MCP Server 를 호출하고 결과를 LLM 에게 전달 함
- 정확한 답변을 위해 LLM 은 MCP Server 를 여러번 호출 할 수 있음
- MCP Server 호출 전에는 사용자 에게 어떤 mcp 를 호출 하는지 설명하고 Y/N 입력을 요청하고 Y 입력시 호출하고 N 입력시 작업을 중단 함.
- MCP 관련 기능은 github.com/modelcontextprotocol/go-sdk 의 mcp 패키지를 이용해 MCP Client 기능을 구현하고 패키지 사용 가이드는 다음 URL 을 참고 할 것
  - https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp
- Default system_prompt.txt 생성 기능 추가
  - 최초 실행 시 system_prompt.txt 파일의 존재 여부를 확인하고 미 존재시 Default system_prompt.txt 를 생성 할 것.
  - system_prompt.txt 에는 MCP server 호출을 위한 tooling 정의가 포함되어야 함.
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Ollama API tooling 지원
- Ollama 에서도 MCP 를 사용 할 수 있도록 API 호출 시 tool 설정 추가
- Ollama chat api request sample
```shell
curl http://localhost:11434/api/chat -d '{
  "model": "llama3.2",
  "messages": [
    {
      "role": "user",
      "content": "what is the weather in tokyo?"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the weather in a given city",
        "parameters": {
          "type": "object",
          "properties": {
            "city": {
              "type": "string",
              "description": "The city to get the weather for"
            }
          },
          "required": ["city"]
        }
      }
    }
  ],
  "stream": false 
}'
```
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Config 파일 Schema 수정
- Config.json 에서 root 의 provider 와 activeModel 은 삭제
- models 의 각 model 설정에 active 항목을 추가하고, true 면 해당 model 이 활성화 된 것으로 처리
- 만약 active true 인 모델이 없을 경우. 사용자가 prompt 입력시 /set-model 커맨드를 통해 모델을 먼저 선택 할 것을 가이드
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Prompt 입력 시 커서 이동 기능 추가
- 좌우 방향키와, Home, End 키를 통해 커서를 이동 시킬 수 있어야 함
- 한국어, 중국어, 일본어 같은 언어 입력시에도 문제 없이 동작 해야 함
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# MCP Session 재사용 기능 추가
- 특정 MCP Server 에 대한 최초의 Tool calling 으로 Session 이 생성되면 이후 동일한 MCP Server 에 대한 Tool calling 시 session 이 살아 있는지 확인하고 살아 있을경우 재 사용 하고, close 되었을 경우 재 생성 하도록 수정.
- 프로그램 종료 시 모든 mcp session 을 close 할 것.
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Thinking message 를 출력 하는 기능 추가
- LLM 으로 수신하는 Thinking 메시지를 streaming 할것
- Thinking 메시지 임을 인지 할 수 있도록 시작과 끝을 구분 할 것
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Windows Terminal 에서 커서 이동 안되는 문제 수정
- 리눅스에서는 사용자 프롬프트 입력시 커서이동이 잘되지만 windows 에서는 동작 안함
- windows 에서도 커서 이동을 할 수 있도록 수정 필요
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# MCP 설정 파일 위치 및 Schema 변경
- 새로운 mcp 설정 파일 위치: $HOME/.humble-ai-cli/mcp-servers.json
- mcp-servers.json 하나의 파일에 여러개의 Mcp 서버를 정의 하는 방식
- command 방식 뿐 아니라 SSE/HTTP URL (remote endpoint) 정의도 지원 할 것
- 다음 MCP server JSON configuration snippet 을 참고 할것.
```json
{
  "mcpServers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_xxx"
      }
    },
    "remote-sse": {
      "url": "https://your-server.example.com/mcp/sse",
      "env": {
        "AUTH_TOKEN": "token"
      }
    }
  }
}
```
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Tool Calling auto/manual 설정 기능 추가
- toolCallMode config 추가
  - config.json 에 toolCallMode 항목을 추가하고 auto 또는 manual 로 설정 하도록 기능 추가
  - auto 로 설정 된 경우 Tool Call 시 현재와 같이 Tool Call 에 대한 메시지를 출력하지만 Call now? 를 통해 사용자에게 실행을 확인하지 않고 자동 실행 함
  - manual 로 설절 된 경우 현재와 같이 Call now? 를 통해 사용자에게 실행을 확인 함
  - 설정값이 없을 경우 default 값은 manual
- 이 설정 값을 변경 할수 있는 command 추가
  - 커맨드: /set-tool-mode [auto|manual]
  - auto 와 manual 이 아닌 다른 값 입력시 auto 와 manual 둘중 하나를 입력 하라는 메시지 출력
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Ollama API 의 tools 를 사용하지 않고 tool schema 를 System Prompt 에 직접 설정 하도록 수정
- 다음과 같이 Tool schema 를 System Prompt 에 추가
```
CALL_FUNCTION:
Never use natural language when you call function.


FUNCTIONS:
[
  {
    "name": "context7__resolve-library-id",
    "description": "Resolves a package/product name to a Context7-compatible library ID and returns a list of matching libraries.",
    "parameters": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "libraryName": { 
          "type": "string", 
          "description": "Library name to search for and retrieve a Context7-compatible library ID." 
        }
      },
      "required": ["libraryName"]
    }
  },
  {
    "name": "context7__get-library-docs",
    "description": "Fetches up-to-date documentation for a library. You must call 'context7__resolve-library-id' first to obtain the exact Context7-compatible library ID required to use this tool, UNLESS the user explicitly provides a library ID in the format '/org/project' or '/org/project/version' in their query.",
    "parameters": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "context7CompatibleLibraryID": { 
          "type": "string", 
          "description": "Exact Context7-compatible library ID (e.g., '/mongodb/docs', '/vercel/next.js', '/supabase/supabase', '/vercel/next.js/v14.3.0-canary.87') retrieved from 'context7__resolve-library-id' or directly from user query in the format '/org/project' or '/org/project/version'." 
        },
        "topic": {
          "type": "string", 
          "description": "Topic to focus documentation on (e.g., 'hooks', 'routing')." 
        }
      },
      "required": ["context7CompatibleLibraryID"]
    }
  }
]

TOOL_CALL:
- Schema
{
	"name": "tool name",
	"arguments": {
	  "arg1 name": "argument1 value",
	  "arg2 name": "argument2 value",
	},
	"reason": "reason why calling this tool"
}
- Example
{
	"name": "context7__resolve-library-id",
	"arguments": {
	  "libraryName": "java"
	},
	"reason": "why this tool call is needed"
}
```

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# Ollama API 의 tool schema 를 System Prompt 에 설정 하는 부분을 다음의 예제와 같이 변경 해줘
---
CALL_FUNCTION:
Never use natural language when you call function.


FUNCTIONS:

# Connected MCP Servers

## context7

### Available Tools
- context7__resolve-library-id: Resolves a package/product name to a Context7-compatible library ID and returns a list of matching libraries.
    Input Schema:
    {
      "type": "object",
      "properties": {
        "libraryName": {
          "type": "string",
          "description": "Library name to search for and retrieve a Context7-compatible library ID."
        }
      },
      "required": [
        "libraryName"
      ],
      "additionalProperties": false,
      "$schema": "http://json-schema.org/draft-07/schema#"
    }

- context7__get-library-docs: Fetches up-to-date documentation for a library. You must call 'context7__resolve-library-id' first to obtain the exact Context7-compatible library ID required to use this tool, UNLESS the user explicitly provides a library ID in the format '/org/project' or '/org/project/version' in their query.
    Input Schema:
    {
      "type": "object",
      "properties": {
        "context7CompatibleLibraryID": {
          "type": "string",
          "description": "Exact Context7-compatible library ID (e.g., '/mongodb/docs', '/vercel/next.js', '/supabase/supabase', '/vercel/next.js/v14.3.0-canary.87') retrieved from 'context7__resolve-library-id' or directly from user query in the format '/org/project' or '/org/project/version'."
        },
        "topic": {
          "type": "string",
          "description": "Topic to focus documentation on (e.g., 'hooks', 'routing')."
        },
        "tokens": {
          "type": "number",
          "description": "Maximum number of tokens of documentation to retrieve (default: 5000). Higher values provide more context but consume more tokens."
        }
      },
      "required": [
        "context7CompatibleLibraryID"
      ],
      "additionalProperties": false,
      "$schema": "http://json-schema.org/draft-07/schema#"
    }
---
**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# Ollama Tool Call 에 대한 JSON 을 Context 에 삽입 시 "tool_calls" 가 아닌 "content" 에 Json 문자열로 설정 할것
- 현재 상태
```json
{
  "role": "assistant",
  "content": "",
  "tool_calls":[
    {
      "type": "function",
      "function": {
        "name": "context7__resolve-library-id",
        "arguments": {
          "libraryName": "fastmcp"
        }
      }
    }
  ]
}
```

- 다음으로 개선 
```json
{
  "role": "assistant",
  "content": "{￦"name￦": ￦"context7__resolve-library-id￦",￦"arguments￦": {￦"libraryName￦": ￦"fastmcp￦"}}"
}
```

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# assistant 의 Tool Call json content와 Tool Call 결과 content 를 context 에서 제외 하도록 수정
- tool call 진행 및 최종 답변 생성시 까지는 지금과 같이 tool call json 및 tool call 결과를 context 로 전달
- 최종 답변을 받아 출력 이후에는 최종 답변만 context 에 유지
- Tool Calling 과정에서의 Context 구성
  1. system: system prompt
  2. previous context
  3. user: search somthing with mcp
  4. assistant: tool call json
  5. tool: tool call result
  6. assistant: final result
- Final result 생성 이후의 context 구성
  1. system: system prompt
  2. previous context
  3. user: search somthing with mcp
  4. assistant: final result
  5. user: new message

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# MCP 실행결과 role 고정
- MCP tool call 결과 메시지의 `role` 값은 항상 `"tool"` 로 설정한다.
- tool 호출 정보를 구분할 때에는 `name` 또는 별도 메타데이터를 활용한다.

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# Default System Prompt 변경

You are a **tool-enabled AI Agent** designed to operate using MCP (Model Context Protocol) servers and tools.
Your primary objective is to achieve the user’s goal efficiently and safely using available tools.

---

## **1) Core Rules**

1. If a user request is determined to require a tool call, invoke the tool declared in the system prompt; otherwise, generate a final response immediately.
   * DO NOT GUESS and call a tool that is not declared in the system prompt.
   * If there is no tool defined in the system prompt, you should determine on your own that there is no tool available to call and respond accordingly.

2. **Do NOT call the same tool with the same arguments more than once.**
   (Deduplicate tool calls to avoid repetition.)

3. **If any tool call returns an error, immediately stop all further tool calls.**

   * Summarize the failure briefly to the user
   * Ask how they would like to proceed (retry, alternative, provide more info)

4. **When necessary, call multiple tools and combine their results into a final answer.**

   * Avoid unnecessary tool calls; only call the tools required for the user's request.

5. **When sending a tool call message, NEVER include natural language.**
   Only send valid tool-call JSON — no explanation, no text around it.

6. **If additional information is needed to perform a tool call, ask the user questions first.**
   Do not guess missing parameters.

7. Before calling a tool, evaluate whether you already have enough information to answer.
   If you do, respond without calling the tool.

8. When providing final answers (not tool calls), include:

   * reasoning summary
   * assumptions or limitations
   * suggested next steps if helpful

9. **Generate the final answer concisely and clearly.**

---

## **2) Tool Call Protocol**

* A tool call message must contain **only the tool invocation** (JSON format).
* Do not combine multiple tool calls in a single message.
* Always check previous tool call history to prevent duplicate calls.

---

## **3) Error Handling Rules**

If a tool call response indicates an error (timeout, invalid response, HTTP error, non-zero exit code, etc.):

You MUST:

1. **Stop making any further tool calls**
2. Return a short summary of the issue
3. Ask the user how to proceed (e.g., retry, provide different input, try alternative tool)

Do NOT expose unnecessary internal details, logs, or stack traces
Provide only concise and relevant information

---

## **4) Multi-Tool Result Synthesis**

When calling more than one tool:

* Validate and cross-check results when possible
* If there is a conflict, explain which result is more reliable and why
* The synthesis/explanation must appear **only in the final natural language answer**, not inside tool calls

---

## **5) Asking the User for Missing Information**

If information is incomplete, ambiguous, or missing, ask **targeted questions only for what is required** before tool calls. Examples:

* “Which browser would you like to use?”
* “Do you already have login credentials?”
* “Which selector should I extract data from?”

Ask minimal questions required to move forward.

---

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# tool name 에 MCP Server name 을 namespace 로 추가
- 기존에 tool name 만 사용하던 것을 tool name 앞에 server name 을 추가
- tool name format: <server_name>__<tool_name> 
- e.g. context7__resolve-library-id

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**

# Tool Call schema 에 reason 추가
- 다음과 같이 reason 를 추가
- REQUIREMENTS.md 와 context 에도 변경 사항 적용

```json
{
  "name": "tool name",
  "arguments": {
    "arg1 name": "argument1 value",
    "arg2 name": "argument2 value",
  },
  "reason": "reason why calling this tool"
}
```

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# MCP Server enabled 설정을 true/false 로 toggle 할 수 있는 command 추가
- command: /toggle-mcp
- mcp-servers.json 에 설정된 mcp server 들의 번호와 server name, 현재 enabled 설정 값이 출력됨
Choose the MCP server to enable/disable (0 to cancel):
  1) context7: enable
  2) playwright: disable
- 여기서 번호를 입력하고 엔터를 누르면 mcp server 설정이 없데이트 됨

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# 활성화된 Tool 이 없는 경우에 대한 프롬프트 처리 강화
- enabled 된 MCP Server 가 없을경우 MCP tool schema 안내 영역에 **NO TOOL CONNECTED** 문구를 출력 할것
- Default system prompt 에 다음 내용을 추가(in English)
"사용자 요청을 위한 적절한 Tool 을 찾을 수 없을 경우 스스로 답변을 생성 할 것"

**LLM_RULES.md 파일에 정의된 Coding rule 을 따를 것.**


# Route Intent 기능 추가
- Tool 호출 시 이제 다음과 같은 흐름으로 진행 하도록 수정
  1. 호출이 필요하다고 판단될 경우 tool name 과 함께 choose-tool 을 호출 합니다. 
  2. 해당 하는 tool 에 대한 input schema 를 전달 받습니다. 
  3. schema에 필요한 property 를 설정해 tool 을 호출 합니다. 
  4. tool 호출 결과를 받습니다.
- 즉 System prompt 에서 MCP tool 의 input schema 는 제거됨
- LLM 이 choose-tool 로 특정 tool 을 선택 하면 이에 대한 응답으로 input schema 를 제공
- FUNCTIONS system prompt 는 이제 다음과 같이 구성됨
```
FUNCTIONS:

# Connected Tools

## Internal Tool: route-intent

- name: **choose-tool**
  - description: Choose tool first which you want call .

    Input Schema:
    {
      "$schema": "http://json-schema.org/draft-07/schema#",
      "additionalProperties": false,
      "properties": {
        "toolName": {
          "description": "The name of the tool that the agent should route to, based on the user’s intent. This value identifies which tool’s Input Schema should be returned for validation before execution.",
          "type": "string"
        }
      },
      "required": [
        "tool"
      ],
      "type": "object"
    }

## MCP Server: context7

- name: **context7__get-library-docs**
- description: Fetches up-to-date documentation for a library. You must call 'resolve-library-id' first to obtain the exact Context7-compatible library ID required to use this tool, UNLESS the user explicitly provides a library ID in the format '/org/project' or '/org/project/version' in their query.

- name: **context7__resolve-library-id**
- description: Resolves a package/product name to a Context7-compatible library ID and returns a list of matching libraries.
```

- context7 MCP 호출에 대한 Message Flow 예제
```json
[
  {
    "role": "user",
    "content": "context7 에서 java object diff 문서를 찾아 사용법을 정리해줘"
  },
  {
  "role": "assistant",
  "content": "{\n  \"name\": \"choose-tool\",\n  \"arguments\": {\n    \"toolName\": \"context7__resolve-library-id\"\n  },\n  \"reason\": \"To find documentation for a Java object diff library, I first need to resolve the correct library ID using the query 'java object diff'.\"\n}"
  },
  {
    "role": "tool",
    "content": "{\r\n      \"$schema\": \"http:\/\/json-schema.org\/draft-07\/schema#\",\r\n      \"additionalProperties\": false,\r\n      \"properties\": {\r\n        \"context7CompatibleLibraryID\": {\r\n          \"description\": \"Exact Context7-compatible library ID (e.g., '\/mongodb\/docs', '\/vercel\/next.js', '\/supabase\/supabase', '\/vercel\/next.js\/v14.3.0-canary.87') retrieved from 'resolve-library-id' or directly from user query in the format '\/org\/project' or '\/org\/project\/version'.\",\r\n          \"type\": \"string\"\r\n        },\r\n        \"tokens\": {\r\n          \"description\": \"Maximum number of tokens of documentation to retrieve (default: 5000). Higher values provide more context but consume more tokens.\",\r\n          \"type\": \"number\"\r\n        },\r\n        \"topic\": {\r\n          \"description\": \"Topic to focus documentation on (e.g., 'hooks', 'routing').\",\r\n          \"type\": \"string\"\r\n        }\r\n      },\r\n      \"required\": [\r\n        \"context7CompatibleLibraryID\"\r\n      ],\r\n      \"type\": \"object\"\r\n    }"
  },
  {
    "role": "assistant",
    "content": "{\n  \"name\": \"context7__get-library-docs\",\n  \"arguments\": {\n    \"context7CompatibleLibraryID\": \"/apache/commons-diff\",\n    \"tokens\": 5000,\n    \"topic\": \"usage\"\n  },\n  \"reason\": \"Based on the query 'java object diff', the most relevant library is Apache Commons Diffs, which provides utilities for comparing objects and generating differences. I am retrieving its documentation to summarize usage.\"\n}"
  }
]
```

