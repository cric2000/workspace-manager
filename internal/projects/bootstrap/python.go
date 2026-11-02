package bootstrap

import (
	"regexp"
	"strconv"
)

var pythonName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func renderPython(options Options) (Plan, error) {
	if !pythonName.MatchString(options.Name) {
		return Plan{}, ErrPythonName
	}
	greeting := "Hello from " + options.Name + "!"
	return Plan{TemplateID: "python", Files: []File{
		{Path: "pyproject.toml", Contents: "[build-system]\nrequires = [\"setuptools>=77.0.3\"]\nbuild-backend = \"setuptools.build_meta\"\n\n[project]\nname = \"" + options.Name + "\"\nversion = \"0.1.0\"\ndescription = \"A standard-library command-line application\"\nreadme = \"README.md\"\nrequires-python = \">=3.11\"\ndependencies = []\n\n[project.scripts]\n" + options.Name + " = \"main:main\"\n\n[tool.setuptools]\npy-modules = [\"main\"]\n"},
		{Path: "main.py", Contents: "def main() -> None:\n    print(" + strconv.Quote(greeting) + ")\n\n\nif __name__ == \"__main__\":\n    main()\n"},
		{Path: "tests/test_main.py", Contents: "from contextlib import redirect_stdout\nfrom io import StringIO\nimport unittest\n\nfrom main import main\n\n\nclass MainTests(unittest.TestCase):\n    def test_greeting(self) -> None:\n        output = StringIO()\n        with redirect_stdout(output):\n            main()\n        self.assertEqual(output.getvalue(), " + strconv.Quote(greeting+"\n") + ")\n\n\nif __name__ == \"__main__\":\n    unittest.main()\n"},
		{Path: ".gitignore", Contents: ".venv/\nvenv/\n__pycache__/\n*.py[cod]\n*.egg-info/\nbuild/\ndist/\n.pytest_cache/\n.coverage\nhtmlcov/\n.env\n.env.*\n!.env.example\n"},
		{Path: "README.md", Contents: "# " + options.Name + "\n\nRequires Python 3.11 or later. Running and testing use only the standard library.\n\n## Run and test\n\n```sh\npython3 -B main.py\npython3 -B -m unittest discover -s tests -v\n```\n\n## Optional virtual environment\n\nCreate an environment only when you choose to. Use a new .venv path; venv can reuse an existing directory. On Ubuntu, python3-venv may need to be installed first.\n\n```sh\npython3 -m venv .venv\n.venv/bin/python -B main.py\n.venv/bin/python -B -m unittest discover -s tests -v\n```\n\nActivation is optional. Workspace Manager saves the selected system Python executable for this standard-library starter. Creating a virtual environment does not automatically change the saved startup command.\n\n## Optional package installation\n\nThe pyproject.toml declares a setuptools build backend and a console entry point. Installing the project may download build tools; do this only when you approve those downloads and have pip in the virtual environment:\n\n```sh\n.venv/bin/python -m pip install .\n.venv/bin/" + options.Name + "\n```\n\nDo not install into the system Python environment. Creation itself does not run Python, create an environment, install dependencies or initialize Git.\n"},
	}, InitializationCommands: []string{}, StartupName: "Run Python application", StartupProgram: "python3", StartupArguments: []string{"-B", "main.py"}, Preview: "Command-line output; no browser preview.", DependencySetup: &Setup{
		Program: "python3", Arguments: []string{"-m", "venv", ".venv"}, Label: "Optional virtual environment", Note: "Optional manual step; not needed to run or test this standard-library starter. Creates .venv and bootstraps pip when available. Use a new destination; venv can reuse existing directories. Ubuntu may require python3-venv. Not run during creation.",
	}}, nil
}
