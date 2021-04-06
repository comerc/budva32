build-image:
	docker build -fDockerfile -tbudva32 .

up:
	docker-compose -fdocker-compose.yml up
