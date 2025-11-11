# Functional Requirements
- CLI 를 통해 LLM 과 대화 기능을 제공 할것
- 대화의 Context 를 유지 할 것
- OpenAI 와 Ollama API 와 연계 할 수 있어야 함
- Ollama API 를 호출할 때 MCP tool schema 는 API `tools` 필드를 사용하지 말고 아래 예제와 같이 System Prompt 에 직접 포함해 전달한다.
  ```
  FUNCTIONS:

  # Connected MCP Servers

  ## context7
  These are tool name, description and input schema.

  - **resolve-library-id**: Resolves a package/product name to a Context7-compatible library ID and returns a list of matching libraries.
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

  - **get-library-docs**: Fetches up-to-date documentation for a library. You must call 'resolve-library-id' first to obtain the exact Context7-compatible library ID required to use this tool, UNLESS the user explicitly provides a library ID in the format '/org/project' or '/org/project/version' in their query.
      Input Schema:
      {
        "type": "object",
        "properties": {
          "context7CompatibleLibraryID": {
            "type": "string",
            "description": "Exact Context7-compatible library ID (e.g., '/mongodb/docs', '/vercel/next.js', '/supabase/supabase', '/vercel/next.js/v14.3.0-canary.87') retrieved from 'resolve-library-id' or directly from user query in the format '/org/project' or '/org/project/version'."
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
  ```
- System prompt 의 마지막에는 다음 FUNCTION_CALL 참고 블록을 추가한다.
  ```
  FUNCTION_CALL:
  - Schema
  {
  	"server": "server name",
  	"name": "function name",
  	"arguments": {
  	  "arg1 name": "argument1 value",
  	  "arg2 name": "argument2 value",
  	}
  }
  - Example
  {
  	"server": "context7",
  	"name": "resolve-library-id",
  	"arguments": {
  	  "libraryName": "java"
  	}
  }
  ```
- 기본 system prompt 는 다음 내용을 정확히 포함해야 한다.
  ```
  You are a **tool-first AI Agent** designed to operate using MCP (Model Context Protocol) servers and tools.
  Your primary objective is to achieve the user’s goal efficiently and safely using available tools.

  ---

  ## **1) Core Rules**

  1. **Do NOT call the same tool with the same arguments more than once.**
     (Deduplicate tool calls to avoid repetition.)

  2. **If any tool call returns an error, immediately stop all further tool calls.**

     * Summarize the failure briefly to the user
     * Ask how they would like to proceed (retry, alternative, provide more info)

  3. **When necessary, call multiple tools and combine their results into a final answer.**

     * Avoid unnecessary tool calls; only call the tools required for the user's request.

  4. **When sending a tool call message, NEVER include natural language.**
     Only send valid tool-call JSON — no explanation, no text around it.

  5. **If additional information is needed to perform a tool call, ask the user questions first.**
     Do not guess missing parameters.

  6. Before calling a tool, evaluate whether you already have enough information to answer.
     If you do, respond without calling the tool.

  7. When providing final answers (not tool calls), include:

     * reasoning summary
     * assumptions or limitations
     * suggested next steps if helpful

  8. **Generate the final answer concisely and clearly.**

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
  ```
- Ollama 모델이 함수 호출 JSON 을 assistant 메시지에 포함(단독 또는 자연어와 혼합)하는 경우 해당 JSON 을 파싱해 MCP tool 을 호출해야 한다.
- MCP tool 호출 결과를 context 에 기록할 때 `role` 필드는 항상 `"tool"` 로 설정한다.
- MCP tool call 진행 중에는 assistant 의 tool call JSON 메시지와 tool 역할의 결과 메시지를 LLM 요청 context 에 포함하지만, 최종 답변이 완료되면 이러한 중간 메시지들은 대화 context 와 히스토리에 포함하지 않고 마지막 assistant 자연어 응답만 남긴다.
- 모든 LLM 호출 시 `temperature` 파라미터는 0.1 로 고정해 전달한다.
- stream true 로 LLM 으로 받은 답변을 순차적으로 화면에 출력 한다.
- 현재 활성화된 model 이 없는 상태에서 질문을 입력하면 /set-model 커맨트를 통해 model 을 선택하도록 가이드 하고, config.json 에 설정된 model 이 없을경우 config.json 에 model 설정을 추가 하라고 가이드 한다.
- 프로그램 실행시 새로운 세션을 메모리상에서만 생성하고 파일로 저장하지 않는다. 대화 세션의 파일 저장은 최초 LLM 으로 부터 답변을 받은 시점 부터 이다.
- 질문을 입력하면 우선 "Waiting for response..." 를 출력한다.
- LLM 으로부터 thinking 메시지를 수신하면 `<<< Thinking >>>` 줄을 출력한 뒤 thinking 내용을 스트리밍으로 표시하고, 종료 시 `<<< End Thinking >>>` 줄을 출력한다.
- LLM 의 답변을 기다리거나 출력 중에 CTRL+C 를 누르면 다시 입력 모드로 돌아 간다.
- 입력 모드에서 CTRL+C 를 누르면 프로그램을 종료 한다.
- 프롬프트 입력 시 좌우 방향키, Home, End 키로 커서를 이동할 수 있어야 하며, 한국어/중국어/일본어 등 다국어 입력에서도 정상 동작해야 한다.

## Config
- API 연계 정보등의 설정은 $HOME/.humble-ai-cli/config.json 파일을 사용 함
- provider 를 설정 할 수 있고 provider 에 따라 설정 항목이 다름
    - openai: model, apiKey
    - ollama: model, baseUrl
- models 의 각 항목에 `active` 플래그를 두고 true 로 설정된 단일 모델을 활성 모델로 간주한다.
- 활성화된 model 을 설정 할 수 있어야 하고 대화시 활성화된 model 을 사용 할 것.
- 활성 모델이 존재하지 않으면 사용자 입력 시 /set-model 커맨드를 안내한다.
- log level 설정: debug, info(default), warn, error
- `toolCallMode` 설정을 추가하고 manual(default) 또는 auto 값을 허용한다.
    - manual 일 경우 MCP tool call 시 사용자에게 실행 여부를 재확인한다.
    - auto 일 경우 tool call 요약을 출력하되 추가 확인 없이 즉시 호출한다.
- system prompt 설정은 $HOME/.humble-ai-cli/system_prompt.txt 파일을 사용 함
  - system_prompt.txt 파일과 내용 존재 할경우 LLM 호출시 system prompt 로 설정해야 함
  - 최초 실행 시 system_prompt.txt 파일의 존재 여부를 확인하고 미 존재시 Default system_prompt.txt 를 생성 할 것.
  - system_prompt.txt 에는 MCP server 호출을 위한 tooling 정의가 포함되어야 함.

## 대화 기록
- 대화 세션은 $HOME/.humble-ai-cli/sessions/ 디렉토리에 각각의 json 파일로 저장 한다.
- 파일명은 날짜와시간으로 시작하고 대화 시작 문구(최대 10글자) 를 연결한 다음 확장자 .json 를 설정 한다.
    - 예: 20251016_162030_대화_제목_이다.json
- /new 커맨드로 새로운 세션을 시작하면 메모리상의 대화 이력과 파일 경로가 초기화되고, 새 세션에서 LLM 으로부터 첫 응답을 받은 시점에 새로운 세션 파일을 생성한다.

## 커맨드
- /command 와 같이 슬래시로 시작하는 컨맨드 기능을 제공한다.
    - /help: 커맨드 리스트와 설명을 보여줌
    - /new: 메모리상의 대화 세션을 초기화하고 이후 입력을 새로운 세션으로 처리한다.
    - /set-model: 설정된 model 리스트를 번호와 함꼐 보여주고 번호를 입력 시 해당 model을 이용해 대화 할 수 있어야 한다. 0을 선택하면 기존 설정을 유지.
    - /mcp: 현재 활성화된 MCP 서버와 각 서버가 제공하는 function 이름과 description 을 출력한다.
    - /set-tool-mode [auto|manual]: MCP tool call 자동 실행 방식을 변경한다. 지원하지 않는 값 입력 시 auto 또는 manual 중 하나를 입력하라고 안내한다.
    - /exit: 프로그램을 종료한다.(CTRL+C 키를 누를 떄와 동일함)

## Logging
- $HOME/.humble-ai-cli/logs 디렉토리에 날짜별 로그파일(application-hac-%d{yyyy-MM-dd}.log) 을 생성하고 기록한다.
- config.json 에 설정된 log level(debug, info, warn, error) 에 따라 로그 출력 여부를 결정한다.
- 다음 이벤트는 debug 레벨로 기록한다.
    - LLM API request 및 response
    - MCP 서버 초기화 과정과 tool 호출 결과

## MCP Server 호출 기능
- MCP Server 설정은 $HOME/.humble-ai-cli/mcp-servers.json 단일 파일에서 관리하며, JSON 구조는 다음을 따른다.
  - 루트에 `mcpServers` 오브젝트를 두고 key 를 MCP 서버 이름으로 사용한다.
  - 각 서버 항목은 `description`, `enabled`(기본값 true), `command`, `args`, `env`, `url`, `transport` 필드를 지원한다.
  - command 기반 서버는 `command` 와 선택적 `args`, `env`(프로세스 환경 변수)를 지정한다.
  - 원격 서버는 `url` 을 지정하고, `transport` 로 `sse`(기본값) 또는 `http`(streamable HTTP) 를 선택할 수 있다.
  - 원격 서버의 `env` 항목은 HTTP 헤더로 전송되어 토큰 등 인증 정보를 전달한다.
  - `command` 와 `url` 중 하나는 반드시 설정되어야 하며, 동시에 둘 다 설정하면 안 된다.
- MCP Server 설정에는 enable/disable 을 설정 할 수 있고 enable 된 MCP Server 만 initialize 하고 호출 할 수 있음
- LLM 이 필요시 MCP Server 호출을 요청 할 수 있고 humble-ai-cli 를 MCP Server 를 호출하고 결과를 LLM 에게 전달 함
- 정확한 답변을 위해 LLM 은 MCP Server 를 여러번 호출 할 수 있음
- MCP Server 는 서버별로 단일 MCP 세션을 유지하며, 세션이 종료되지 않았다면 재사용하고 종료된 경우에만 재연결 할 것
- MCP Server 호출 전에는 사용자 에게 어떤 mcp 를 호출 하는지 설명하고 Y/N 입력을 요청하고 Y 입력시 호출하고 N 입력시 작업을 중단 함.
- 프로그램 종료 시 활성화 되어 있는 모든 MCP 세션을 정상적으로 close 할 것

## Log file
- $HOME/.humble-ai-cli/logs 디렉토리에 날짜별 로그파일을 생성한다.
  - 로그파일명 포맷: application-hac-%d{yyyy-MM-dd}.log
- config.json 에 설정된 log level 에 따라 로그 출력

# Non-Functional Requirements
- 개발 언어: go 1.25.2
- MCP 관련 기능은 github.com/modelcontextprotocol/go-sdk 의 mcp 패키지를 이용해 MCP Client 기능을 구현하고 패키지 사용 가이드는 다음 URL 을 참고 할 것
    - https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp
