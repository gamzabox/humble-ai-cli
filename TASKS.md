# 첫번째 기능 구현 프롬프트
- [x] REQUIREMENTS.md와 LLM_RULES.md를 검토하고 작업 계획을 수립했다.
- [x] 요구사항을 검증하는 테스트 코드를 작성한다.
- [x] 테스트를 통과하도록 CLI 기능을 구현한다.
- [x] 모든 테스트를 실행해 통과 여부를 확인하고 필요한 문서를 갱신한다.

# 새 세션 생성 커맨트 추가
- [x] /new 커맨드 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] /new 커맨드 동작을 검증하는 테스트를 추가한다.
- [x] /new 커맨드 구현을 완료한다.
- [x] 전체 테스트를 실행하고 문서 업데이트를 검증한다.

# MCP Server 호출 기능 추가
- [x] 요구사항을 검증하는 테스트를 작성한다.
- [x] MCP 연동 및 system prompt 초기화 로직을 구현한다.
- [x] 전체 테스트를 실행하고 필요한 문서를 갱신한다.

# MCP 커맨드 추가
- [x] /mcp 커맨드 요구사항을 테스트로 정의한다.
- [x] MCP function 리스트 출력 기능을 구현한다.
- [x] 전체 테스트를 실행하고 문서를 갱신한다.

# Logging 기능 추가
- [x] 요구사항을 검증하는 테스트를 작성한다.
- [x] 로그 구성 및 기록 기능을 구현한다.
- [x] 전체 테스트를 실행하고 문서를 갱신한다.

# Ollama MCP Tooling 지원
- [x] Ollama MCP tool 전달 요구사항을 검증하는 테스트를 추가한다.
- [x] Ollama provider 에 MCP tool 호출 처리 로직을 구현한다.
- [x] 전체 테스트를 실행하고 관련 문서를 업데이트한다.

# Config Schema 수정
- [x] 새로운 config schema 요구사항을 테스트로 정의한다.
- [x] 모델 활성화 플래그 기반 config 로직과 /set-model 안내를 구현한다.
- [x] 전체 테스트를 실행하고 문서를 갱신한다.

# MCP Session 재사용
- [x] MCP 세션 재사용 및 종료 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] MCP 세션 재사용 및 종료 동작을 검증하는 테스트를 작성한다.
- [x] MCP Manager 에 세션 캐싱과 종료 로직을 구현하고 App 종료 시 모든 세션을 닫는다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# Thinking 메시지 스트리밍 표시
- [x] Thinking 메시지 스트리밍 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] Thinking 메시지 스트리밍 동작을 검증하는 테스트를 먼저 작성한다.
- [x] Thinking 메시지 스트리밍 구현을 완료한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)
- [x] OpenAI/Ollama reasoning payload 를 파싱해 실제 thinking 내용을 스트리밍 한다.
- [x] reasoning 스트리밍 동작을 테스트로 검증한다.

# Tool call 로그 보강
- [x] Tool call 수행 시 LLM request 로그 누락 문제를 파악한다.
- [x] Tool call 이후 LLM Request/Response 로그 기록을 검증하는 테스트를 추가한다.
- [x] Tool call 이후 LLM Request/Response 로그가 남도록 구현을 보강한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# 프롬프트 커서 이동 기능
- [x] 프롬프트 입력 커서 이동 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] 커서 이동 및 다국어 입력 동작을 검증하는 테스트를 추가한다.
- [x] 커서 이동 기능을 구현한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# Windows Terminal 커서 이동 문제 수정
- [x] Windows 터미널 커서 이동 요구사항을 검토하고 필요시 REQUIREMENTS.md를 업데이트한다.
- [x] Windows 입력 시퀀스에 대한 커서 이동 테스트를 추가한다.
- [x] Windows 입력 시퀀스를 처리하도록 구현을 수정한다.
- [x] 전체 테스트를 실행해 통과 여부를 확인한다. (`go test ./...`)

# MCP 설정 파일 통합 및 Remote 지원
- [x] REQUIREMENTS.md/README.md에 단일 mcp-servers.json 및 remote 연결 요구사항을 반영한다.
- [x] 새로운 schema와 remote 연결 방식을 검증하는 테스트를 추가한다.
- [x] MCP Manager가 새 schema를 로드하고 command/SSE/HTTP 연결을 처리하도록 구현한다.
- [x] `go test ./...` 를 실행해 전체 테스트를 통과시킨다.

# Tool Call Mode 설정
- [x] tool call mode 요구사항을 REQUIREMENTS.md/README.md/TASKS.md에 반영한다.
- [x] tool call mode 설정과 /set-tool-mode 커맨드를 검증하는 테스트를 추가한다.
- [x] tool call mode 설정 및 커맨드 구현을 완료한다.
- [x] `go test ./...` 로 전체 테스트를 실행해 통과시킨다.

# Ollama Tool Schema Prompt 삽입
- [x] Ollama tool schema 전달 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] Ollama tool schema 프롬프트 삽입 동작을 검증하는 테스트를 먼저 작성한다.
- [x] Ollama provider가 tools 필드 대신 system prompt에 schema를 삽입하도록 구현한다.
- [x] `go test ./...` 를 실행해 전체 테스트를 통과시킨다.

# Ollama Manual Tool Call 파싱
- [x] Ollama 함수 호출 JSON 파싱 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] JSON 형태의 tool call 응답을 처리하는 테스트를 추가한다.
- [x] Ollama provider가 manual function call JSON 을 MCP tool 호출로 변환하도록 구현한다.
- [x] `go test ./...` 를 실행해 전체 테스트를 통과시킨다.

# Ollama Tool Schema 포맷 갱신
- [x] 새로운 시스템 프롬프트 포맷 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] Ollama 요청에 포함되는 시스템 프롬프트가 새 포맷을 따르는지 테스트를 업데이트한다.
- [x] 시스템 프롬프트 생성 로직을 수정하고 전체 테스트(`go test ./...`)를 통과시킨다.

# FUNCTION_CALL 안내 블록 복원
- [x] FUNCTION_CALL 안내 블록 추가 요구사항을 REQUIREMENTS.md에 반영한다.
- [x] FUNCTION_CALL 블록 삽입을 검증하는 테스트를 업데이트한다.
- [x] FUNCTION_CALL 블록을 생성 로직에 추가하고 `go test ./...` 를 통과시킨다.

# Ollama Tool Call Context JSON
- [x] 새로운 컨텍스트 요구사항을 검사하는 테스트를 추가한다.
- [x] Ollama 컨텍스트에 tool_calls 대신 content JSON 을 사용하도록 구현한다.
- [x] `go test ./...` 를 실행해 전체 테스트를 통과시킨다.

# Tool Call Context 정리
- [x] REQUIREMENTS.md 에 tool call context 정리 요구사항을 반영한다.
- [x] tool call context 제외 동작을 검증하는 테스트를 추가한다.
- [x] Tool call 중간 메시지를 context 에서 제거하는 구현을 추가한다.
- [x] `go test ./...` 를 실행해 전체 테스트를 통과시킨다.
