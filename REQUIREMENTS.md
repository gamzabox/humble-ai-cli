# Functional Requirements
- CLI 를 통해 LLM 과 대화 기능을 제공 할것
- 대화의 Context 를 유지 할 것
- OpenAI 와 Ollama API 와 연계 할 수 있어야 함
- stream true 로 LLM 으로 받은 답변을 순차적으로 화면에 출력 한다.
- 현재 활성화된 model 이 없는 상태에서 질문을 입력하면 /set-model 커맨트를 통해 model 을 선택하도록 가이드 하고, config.json 에 설정된 model 이 없을경우 config.json 에 model 설정을 추가 하라고 가이드 한다.
- 프로그램 실행시 새로운 세션을 메모리상에서만 생성하고 파일로 저장하지 않는다. 대화 세션의 파일 저장은 최초 LLM 으로 부터 답변을 받은 시점 부터 이다.
- 질문을 입력하면 우선 "Waiting for response..." 를 출력한다.
- LLM 이 thinking 중이면 "Thinking..." 을 출력한다.
- LLM 의 답변을 기다리거나 출력 중에 CTRL+C 를 누르면 다시 입력 모드로 돌아 간다.
- 입력 모드에서 CTRL+C 를 누르면 프로그램을 종료 한다.

## Config
- API 연계 정보등의 설정은 $HOME/.config/humble-ai-cli/config.json 파일을 사용 함
- provider 를 설정 할 수 있고 provider 에 따라 설정 항목이 다름
    - openai: model, apiKey
    - ollama: model, baseUrl
- 활성화된 model 을 설정 할 수 있어야 하고 대화시 활성화된 model 을 사용 할 것.
- system prompt 설정은 $HOME/.config/humble-ai-cli/system_prompt.txt 파일을 사용 함
- system_prompt.txt 파일과 내용 존재 할경우 LLM 호출시 system prompt 로 설정해야 함

## 대화 기록
- 대화 세션은 실행파일 위치에 chat_history 디렉토리를 생성하고 여기에 각각의 json 파일로 저장 한다.
- 파일명은 날짜와시간으로 시작하고 대화 시작 문구(최대 10글자) 를 연결한 다음 확장자 .json 를 설정 한다.
    - 예: 20251016_162030_대화_제목_이다.json

## 커맨드
- /command 와 같이 슬래시로 시작하는 컨맨드 기능을 제공한다.
    - /help: 커맨드 리스트와 설명을 보여줌
    - /set-model: 설정된 model 리스트를 번호와 함꼐 보여주고 번호를 입력 시 해당 model을 이용해 대화 할 수 있어야 한다. 0을 선택하면 기존 설정을 유지.
    - /exit: 프로그램을 종료한다.(CTRL+C 키를 누를 떄와 동일함)

# Non-Functional Requirements
- 개발 언어: go 1.25.2

