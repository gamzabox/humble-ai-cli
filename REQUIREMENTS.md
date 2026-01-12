# Functional Requirements
- CLI 를 통해 LLM 과 대화 기능을 제공 할것
- 대화의 Context 를 유지 할 것
- 하나의 대화 Context 를 chunk 로 분할하는 기능은 Ollama 모델에만 적용하며 `config.json` 의 `ollamaContextChunkSize` 값이 양수로 설정된 경우에만 동작한다. 설정하지 않거나 0 이하일 경우 chunking 을 수행하지 않는다. chunking 활성화 시 BPE 계열 tokenizer 로 token 수를 측정해 `ollamaContextChunkSize` 토큰 단위로 순차 chunk 를 만들어 context 에 추가 할 것.
- OpenAI provider 를 사용하는 모델은 chunking 과 호환되지 않으므로 `ollamaContextChunkSize` 설정과 무관하게 항상 chunking 을 비활성화한다.
- `config.json` 의 `contextRetentionTurns` 값으로 user prompt 전송 시 과거 context 를 몇 turn 까지 포함할지 제어한다. 값을 설정하지 않으면 기본 3 turn 을 유지하고, 0 이면 직전 context 없이 현재 user 입력만 전송한다. 음수이면 전체 history 를 전송한다. turn 은 user-assistant message 쌍을 의미하지만 session history 에 user content 만 존재하고 assistant 가 없을 경우 해당 user 만 하나의 turn 으로 간주한다.
- OpenAI 와 Ollama API 와 연계 할 수 있어야 함
- Ollama API 를 호출할 때 MCP tool List 는 API `tools` 필드를 사용하지 말고 system prompt 에 직접 포함해 전달한다.
- 활성화된 MCP Server 가 없을 경우 MCP tool schema 프롬프트 영역에는 `NO FUNCTION CONNECTED` 문구를 출력해 툴 목록 대신 안내한다.
- MCP tool input schema 는 System prompt 에 포함하지 않고, LLM 이 `chooseFunction` 을 호출해 schema 를 요청할 때 tool 별 schema 를 전달한다.
- MCP tool name 은 `<server_name>__<tool_name>` 포맷으로 서버 이름을 네임스페이스로 포함해야 한다.
- OpenAI API 호출 시 `tools` 필드에는 `chooseFunction` 을 제외한 MCP tool 의 `parameters`(input schema) 를 포함하지 않는다. 모든 MCP tool schema 는 `chooseFunction` 호출을 통해 전달받는다.
- MCP Tool name 과 description 을 다음과 같이 생성 하고 가장 아래에 Function Call Schema and Example 도 추가한다.
- 이 내용은 system prompt 하단에 포함해 함께 전달한다.

```
# Connected Tools

## MCP Server: awesome-mcp

- function name: **awesome-mcp__get-good-thing**
- description: description about get-good-thing function.

- function name: **awesome-mcp__search-good-thing**
- description: description about search-good-thing function.


# Function Call Schema and Example
## Schema
{
  "functionCall": {
    "server": "server_name",
    "name": "server_name__tool name",
    "arguments": {
      "arg1 name": "argument1 value",
      "arg2 name": "argument2 value",
    },
    "reason": "reason why calling this tool"
  }
}

## Example
{
  "functionCall": {
    "server": "good-server",
    "name": "good-server__good-tool",
    "arguments": {
      "goodArg": "nice"
    },
    "reason": "why this tool call is needed"
  }
}

```

- 다음은 Humble AI CLI 가 코드에 내장해 항상 사용하는 기본 system prompt 로, 아래 내용을 정확히 포함해야 한다.
```
You are a **tool-enabled Humble AI Agent** operating with MCP (Model Context Protocol) servers.  
A **tool** corresponds to an MCP server, and a **function** is an action exposed by that tool.

Your goal is to achieve the user’s intent **safely, accurately, and efficiently using available functions**.

---

# 1) Core Behavior Rules
1. **Call a function only when required by the user's request.**  
   If a function is unnecessary, provide a natural-language answer immediately.
2. **Never call a function that is not declared in the system prompt.**  
   If no functions are available, answer the user directly.
3. **Do NOT call the same function with the same arguments more than once.**
4. **If ANY function call returns an error:**
   - stop calling functions  
   - summarize the issue briefly  
   - ask the user how to continue (retry, alternative, more info)
5. **Function-call messages must contain ONLY valid JSON for the call.**  
   No natural language before or after it.
6. **Ask the user for missing information before calling functions.**  
   Do not guess required parameters.
7. **If you already have enough information to answer, do not call a function.**
8. Final natural-language answers (not function calls) must include:
   - short reasoning summary  
   - assumptions or limitations  
   - optional next steps  
9. Keep final answers **clear and concise**.

---

# 2) Function Selection Flow (chooseFunction MUST be used)
Before calling EACH MCP function:
1. Call **chooseFunction** with:
   - `functionName`: the selected function (set only function name)
   - `reason`: why this function is necessary  
2. Receive that function’s input schema.
3. Create a function call using the schema and required properties.
4. Wait for its response and incorporate results into the final answer. If more function call is needed then starts function selection flow again.

---

# 3) Function Call Protocol
- One message = one function call JSON only.
- Do NOT combine multiple calls in the same message.
- Hyphen `-` and underscore `_` are different characters. NEVER interchange them.
- Review previous calls to avoid duplication.

---

# 4) Error Handling
If a function response contains an error:
1. Stop all further function calls
2. Provide a short and user-friendly summary
3. Ask how they want to proceed
Do not reveal internal logs or stack traces; keep it simple and relevant.

---

# 5) Handling Multiple Functions
If multiple functions are used:
- Validate and cross-check results when possible
- Explain conflicts using natural-language only in the final answer
- Do not mix any explanation into function call messages

---

# 6) Asking for Missing Information
When user input is incomplete or ambiguous, ask for only what is strictly necessary to proceed.
Examples:
- “Which browser should I use?”
- “Do you have login credentials?”
- “Which selector should I extract data from?”
Ask minimal questions required to make the next legitimate function call.

---

# Choose Function Example
{
  "chooseFunction": {
    "functionName": "doAwesome",
    "reason": "Need to perform the awesome action"
  }
}

---

```
- Ollama 모델 요청에는 system prompt 하단에 `# Connected Tools` 와 `# Function Call Schema and Example` 안내를 이어 붙여 전달하고, 연결된 MCP tool 이 없으면 `NO FUNCTION CONNECTED` 문구를 포함한다.
- OpenAI 모델 요청에는 `# Connected Tools` 와 `# Function Call Schema and Example` 안내를 모두 생략한다. 단, 연결된 MCP tool 이 없으면 `NO FUNCTION CONNECTED` 문구만 별도로 출력해 도구 부재를 알린다.
- Connected tools 안내 하단에 현재 시스템 정보를 다음 포맷으로 추가한다.

```
# System Information
- OS: <OS Name and version>
- Architecture: <amd64, arm64, etc>
- Locale: <locale>
- Timezone: <Timezone>
- Datetime: <ISO format datetime of now>
```

- Ollama 모델이 함수 호출 JSON 을 assistant 메시지에 포함(단독 또는 자연어와 혼합)하는 경우 해당 JSON 을 파싱해 MCP tool 을 호출해야 한다.
- MCP tool 호출 결과를 context 에 기록할 때 `role` 필드는 항상 `"tool"` 로 설정한다.
- MCP tool call 진행 중에는 assistant 의 tool call JSON 메시지와 tool 역할의 결과 메시지를 LLM 요청 context 에 포함하지만, 최종 답변이 완료되면 이러한 중간 메시지들은 대화 context 와 히스토리에 포함하지 않고 마지막 assistant 자연어 응답만 남긴다.
- 모든 LLM 호출 시 기본적으로 `temperature` 파라미터를 0.1 로 설정해 전달하되, OpenAI 모델이 해당 값을 지원하지 않아 400 에러를 반환하면 `temperature` 필드를 제거하고 기본값(1)으로 즉시 재시도한다.
- stream true 로 LLM 으로 받은 답변을 순차적으로 화면에 출력 한다.
- 프로그램 실행 직후 CLI 는 현재 buildinfo.Version 값을 포함해 아래 순서대로 3줄 안내 메시지를 항상 출력한다.
  ```
  Humble AI CLI version <version>
  - Use /help for detailed commands.
  - Press CTRL+C to stop, CTRL+D to exit.
  ```
- 현재 활성화된 model 이 없는 상태에서 질문을 입력하면 /set-model 커맨트를 통해 model 을 선택하도록 가이드 하고, config.json 에 설정된 model 이 없을경우 config.json 에 model 설정을 추가 하라고 가이드 한다.
- 프로그램 실행시 새로운 세션을 메모리상에서만 생성하고 파일로 저장하지 않는다. 대화 세션의 파일 저장은 최초 LLM 으로 부터 답변을 받은 시점 부터 이다.
- 질문을 입력하면 우선 "Waiting for response..." 를 출력한다.
- LLM 으로부터 thinking 메시지를 수신하면 `<<< Thinking >>>` 줄을 출력한 뒤 thinking 내용을 스트리밍으로 표시하고, 종료 시 `<<< End Thinking >>>` 줄을 출력한다.
- LLM 의 답변을 기다리거나 출력 중에 CTRL+C 를 누르면 다시 입력 모드로 돌아 간다.
- 입력 모드에서 CTRL+C 를 누르면 프로그램을 종료하지 않고 `Press CTRL+D to exit the program.` 안내 메시지를 출력한 뒤 입력을 계속 기다린다.
- 입력 대기 상태에서 CTRL+D 를 누르면 프로그램을 종료한다.
- 프롬프트 입력 시 좌우 방향키, Home, End 키로 커서를 이동할 수 있어야 하며, 한국어/중국어/일본어 등 다국어 입력에서도 정상 동작해야 한다.
- Windows 의 기본 cmd.exe 및 PowerShell 콘솔에서도 동일한 커서 이동과 히스토리 탐색 동작이 가능하도록 ANSI 제어 시퀀스를 처리할 수 있는 출력 모드를 자동으로 활성화한다.
- 입력 중 상/하 방향키로 동일 세션의 이전 입력을 탐색할 수 있어야 하며, 상 방향키는 더 과거의 입력을 순차적으로 불러오고 하 방향키는 다시 최신 입력으로 이동한다. 히스토리를 탐색하다가 하 방향키로 최신 위치로 돌아오면 탐색 시작 직전까지 작성 중이던 내용이 그대로 복원되어야 한다.
- `humble-ai-cli exec <workflow-file.md>` 명령으로 워크플로우 파일을 실행할 수 있어야 하며, 파일에 정의된 사용자 프롬프트들을 단일 세션으로 순차 실행한 뒤 종료한다. 워크플로우 파일이 없거나 필수 정보가 누락/잘못된 경우 에러 메시지를 출력하고 바로 종료한다.
- 워크플로우 파일 포맷은 `WORKFLOW.test.md` 를 따른다. `# CONFIGS` 섹션의 `## Basic Config` JSON 은 `config.json` 을 대체하고, `## MCP Servers` JSON 은 `mcp-servers.json` 을 대체한다. 둘 중 하나가 누락되면 각각 `$HOME/.humble-ai-cli/config.json` 과 `$HOME/.humble-ai-cli/mcp-servers.json` 을 그대로 사용한다.
- 워크플로우 파일에 Basic Config 가 존재할 때는 해당 JSON 필드만 사용하며 누락된 항목은 프로그램 기본값을 적용한다. 최소 한 개의 활성 모델이 존재해야 하며, OpenAI 모델은 `apiKey` 가, Ollama 모델은 `baseUrl` 이 반드시 있어야 한다. 필수 값이 없으면 에러를 출력하고 종료한다(기존 config 파일로 보완하지 않는다).
- 워크플로우 실행 시 `WORKFLOWS` 하위 항목의 본문이 사용자 프롬프트로 사용되며 헤딩 텍스트는 포함하지 않는다. 각 프롬프트는 동일 세션 컨텍스트를 유지한 채 순서대로 실행된다.
- `##` 헤딩으로 시작한 프롬프트의 본문에는 `###` 등의 하위 헤딩이 포함될 수 있으며, 이 내용은 모두 같은 사용자 프롬프트에 속한다. 새로운 프롬프트는 오직 다음 `##` 헤딩에서만 시작한다.
- 워크플로우 모드에서는 각 프롬프트별 최종 답변만 출력하고 시작 안내, 대기 메시지, MCP 호출 요약 등 중간 가이드는 출력하지 않는다. 단, `toolCallMode` 가 `manual` 인 경우 MCP 호출 요약과 Y/N 질의로 사용자 의사를 확인한 뒤 진행한다.

## Config
- API 연계 정보등의 설정은 $HOME/.humble-ai-cli/config.json 파일을 사용 함
- provider 를 설정 할 수 있고 provider 에 따라 설정 항목이 다름
    - openai: model, apiKey
    - ollama: model, baseUrl
- models 의 각 항목에 `active` 플래그를 두고 true 로 설정된 단일 모델을 활성 모델로 간주한다.
- 활성화된 model 을 설정 할 수 있어야 하고 대화시 활성화된 model 을 사용 할 것.
- 활성 모델이 존재하지 않으면 사용자 입력 시 /set-model 커맨드를 안내한다.
- log level 설정: debug, info(default), warn, error
- `ollamaNumCtx` 값을 config.json 루트에 설정하면 Ollama chat API 요청의 `options.num_ctx` 에 전달해 모델 컨텍스트 길이를 제한한다. 설정하지 않거나 0 이하인 경우 기본값 30000을 `num_ctx` 로 전달한다.
- `toolCallMode` 설정을 추가하고 manual(default) 또는 auto 값을 허용한다.
    - manual 일 경우 MCP tool call 시 사용자에게 실행 여부를 재확인한다.
    - auto 일 경우 tool call 요약을 출력하되 추가 확인 없이 즉시 호출한다.
- system prompt 는 코드에 내장된 기본 문자열을 사용하며, 위에서 정의한 Default system prompt 내용을 항상 포함해야 한다.
- 프로그램 실행 시 $HOME/.humble-ai-cli/user-rules.md 파일이 없으면 빈 파일로 생성하고, 해당 파일의 내용을 system prompt 마지막에 그대로 이어 붙여 LLM 에 전달한다.

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
    - /mcp: 현재 활성화된 MCP 서버와 각 서버가 제공하는 tool 이름과 description 을 출력한다.
    - /toggle-mcp: mcp-servers.json 에 등록된 MCP 서버 리스트를 번호와 함께 출력하고 현재 enabled 상태를 표시한다. 번호를 선택하면 해당 서버의 enabled 값을 반전하여 파일에 저장하고, 0을 입력하면 취소한다. 설정이 변경되면 CLI 는 즉시 갱신된 enabled 상태를 반영한다.
    - /set-tool-mode [auto|manual]: MCP tool call 자동 실행 방식을 변경한다. 지원하지 않는 값 입력 시 auto 또는 manual 중 하나를 입력하라고 안내한다.
    - /exit: 프로그램을 종료한다. (입력 모드에서 CTRL+D 를 누르는 것과 동일하게 종료된다.)

## Logging
- $HOME/.humble-ai-cli/logs 디렉토리에 날짜별 로그파일(application-hac-%d{yyyy-MM-dd}.log) 을 생성하고 기록한다.
- config.json 에 설정된 log level(debug, info, warn, error) 에 따라 로그 출력 여부를 결정한다.
- CLI 가 panic 으로 종료되는 등 runtime 에러가 발생하면 에러 메시지와 stack trace 를 포함한 상세 로그를 error 레벨로 기록하고 로그 파일에 남긴다.
- MCP 초기화 실패나 MCP tool 호출 오류(네트워크 실패, 응답 에러 등) 발생 시 해당 원인과 세부 정보를 error 레벨로 로그 파일에 기록한다.
- 다음 이벤트는 debug 레벨로 기록한다.
    - LLM API request 및 response
    - MCP 서버 초기화 과정과 tool 호출 결과

## MCP Server 호출 기능
- MCP Server 설정은 $HOME/.humble-ai-cli/mcp-servers.json 단일 파일에서 관리하며, JSON 구조는 다음을 따른다.
  - 루트에 `mcpServers` 오브젝트를 두고 key 를 MCP 서버 이름으로 사용한다.
  - 각 서버 항목은 `description`, `enabled`(기본값 true), `command`, `args`, `env`, `url`, `type` 필드를 지원한다.
  - `type` 에는 `stdio`, `sse`, `streamable-http` 값을 사용할 수 있다.
  - command 기반 서버는 `command` 와 선택적 `args`, `env`(프로세스 환경 변수)를 지정하고, `type` 은 생략하거나 `stdio` 로 지정한다.
  - 원격 서버는 `url` 을 지정하고, `type` 에 `sse` 또는 `streamable-http` 를 설정해야 한다. `command` 와 `url` 을 모두 설정한 경우 `type` 값에 따라 사용할 전송 방식을 선택한다(`stdio` → command, `sse`/`streamable-http` → url).
  - 원격 서버의 `env` 항목은 HTTP 헤더로 전송되어 토큰 등 인증 정보를 전달한다. `sse` 로 설정했지만 서버가 SSE `endpoint` 이벤트를 제공하지 않으면 CLI 가 자동으로 streamable HTTP 로 재시도한다.
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

## Build & Release
- `build.sh` 스크립트를 제공해 cross-platform 빌드를 수행한다.
- 스크립트는 `git describe --tags --abbrev=0` 결과를 version 값으로 사용하고, 태그가 없을 경우 `dev` 를 사용한다.
- build 시 ldflags `-X` 옵션으로 `github.com/gamzabox/humble-ai-cli/internal/buildinfo.Version` 과 `github.com/gamzabox/humble-ai-cli/internal/buildinfo.Date` 값을 각각 최신 git tag, UTC 빌드 시간(ISO8601/RFC3339 형식)으로 주입한다.
- `internal/buildinfo` 의 version 기본값은 `dev`, date 기본값은 `unknown` 이어야 한다.
- 빌드 타겟은 `linux/amd64`, `windows/amd64`, `windows/arm64` 이며 출력 파일명은 각각 `humble-ai-cli`, `humble-ai-cli_amd64.exe`, `humble-ai-cli_arm64.exe` 여야 한다.
- 생성된 바이너리는 `dist/` 디렉터리에 저장한다.

# Non-Functional Requirements
- 개발 언어: go 1.25.2
- MCP 관련 기능은 github.com/modelcontextprotocol/go-sdk 의 mcp 패키지를 이용해 MCP Client 기능을 구현하고 패키지 사용 가이드는 다음 URL 을 참고 할 것
    - https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp
