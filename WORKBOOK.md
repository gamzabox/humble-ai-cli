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