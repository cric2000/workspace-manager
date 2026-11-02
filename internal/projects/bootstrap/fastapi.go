package bootstrap

import (
	"fmt"
	"strconv"

	"workspace-manager/internal/domain"
)

func renderFastAPI(options Options) (Plan, error) {
	if !pythonName.MatchString(options.Name) {
		return Plan{}, ErrPythonName
	}
	greeting := strconv.Quote("Hello from " + options.Name + "!")
	return Plan{TemplateID: "fastapi", Files: []File{
		{Path: "pyproject.toml", Contents: fmt.Sprintf(`[build-system]
requires = ["setuptools>=77.0.3"]
build-backend = "setuptools.build_meta"

[project]
name = "%s"
version = "0.1.0"
description = "A localhost FastAPI application"
readme = "README.md"
requires-python = ">=3.11"
dependencies = ["fastapi==0.162.0", "uvicorn==0.54.0"]

[project.optional-dependencies]
test = ["httpx2==2.16.0"]

[tool.setuptools.packages.find]
include = ["app*"]
`, options.Name)},
		{Path: "app/__init__.py", Contents: ""},
		{Path: "app/main.py", Contents: fmt.Sprintf(`from fastapi import FastAPI

app = FastAPI(title=%s)


@app.get("/")
def root() -> dict[str, str]:
    return {"message": %s}


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}
`, strconv.Quote(options.Name), greeting)},
		{Path: "tests/test_api.py", Contents: fmt.Sprintf(`import unittest

from fastapi.testclient import TestClient

from app.main import app


class APITests(unittest.TestCase):
    def setUp(self) -> None:
        self.client = self.enterContext(TestClient(app))

    def test_health(self) -> None:
        response = self.client.get("/health")
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json(), {"status": "ok"})

    def test_greeting(self) -> None:
        self.assertEqual(self.client.get("/").json(), {"message": %s})

    def test_docs_and_schema(self) -> None:
        self.assertEqual(self.client.get("/docs").status_code, 200)
        self.assertIn("/health", self.client.get("/openapi.json").json()["paths"])


if __name__ == "__main__":
    unittest.main()
`, greeting)},
		{Path: ".gitignore", Contents: ".venv/\n__pycache__/\n*.py[cod]\n*.egg-info/\nbuild/\ndist/\n.pytest_cache/\n.coverage\n.env\n.env.*\n!.env.example\n"},
		{Path: "README.md", Contents: "# " + options.Name + "\n\nRequires Python 3.11 or later with venv and pip. On Ubuntu, install python3-venv if environment creation is unavailable.\n\n## Set up once\n\nUse a new .venv directory: venv may reuse an existing one. These manual commands download dependencies and build tools into the virtual environment.\n\n```sh\npython3 -m venv .venv\n.venv/bin/python -m pip install '.[test]'\n```\n\n## Run and test\n\n```sh\n.venv/bin/python -B -m uvicorn app.main:app --host 127.0.0.1 --port 8000 --reload\n# In another terminal:\n.venv/bin/python -B -m unittest discover -s tests -v\n```\n\nOpen http://127.0.0.1:8000/docs. The health endpoint is http://127.0.0.1:8000/health. Port 8000 must be free; update both the command and saved preview/readiness settings if changing it. Reload mode is for development.\n\nWorkspace Manager saves the absolute .venv/bin/python invocation, a health check and the docs preview. Complete setup before reviewing and approving that command. Creation does not execute Python, install dependencies, initialize Git, start the server, check readiness or open a browser. Do not install into system Python.\n"},
	}, InitializationCommands: []string{}, StartupName: "Run FastAPI development server", StartupProjectExecutable: ".venv/bin/python",
		StartupArguments: []string{"-B", "-m", "uvicorn", "app.main:app", "--host", "127.0.0.1", "--port", "8000", "--reload"},
		Preview:          "API docs at http://127.0.0.1:8000/docs after manual setup and an approved start. Port 8000 must be available.", PreviewURL: "http://127.0.0.1:8000/docs",
		Runtime:         &domain.CommandRuntime{Readiness: &domain.ReadinessCheck{Kind: "http", URL: "http://127.0.0.1:8000/health", ExpectedStatus: 200, TimeoutSeconds: 30, IntervalSeconds: 1}, Stop: &domain.StopPolicy{Signal: "SIGTERM", GraceSeconds: 10}},
		DependencySetup: &Setup{Program: "python3", Arguments: []string{"-m", "venv", ".venv"}, Label: "Required virtual environment", Note: "First manual step: creates .venv and bootstraps pip. Use a new directory; Ubuntu may require python3-venv. Not run during creation."},
		AdditionalSetup: []Setup{{ProjectExecutable: ".venv/bin/python", Arguments: []string{"-m", "pip", "install", ".[test]"}, Label: "Install FastAPI dependencies", Note: "Second manual step, after creating .venv. Downloads runtime/test dependencies and build tools into the virtual environment. This executable does not exist until the first step succeeds. Not run during creation."}},
	}, nil
}
