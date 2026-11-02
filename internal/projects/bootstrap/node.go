package bootstrap

import (
	"encoding/json"
	"regexp"
	"strconv"
)

var nodeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func renderNode(options Options) (Plan, error) {
	if !nodeName.MatchString(options.Name) {
		return Plan{}, ErrNodeName
	}
	manifest := struct {
		Name            string            `json:"name"`
		Version         string            `json:"version"`
		Private         bool              `json:"private"`
		Type            string            `json:"type"`
		Engines         map[string]string `json:"engines"`
		Scripts         map[string]string `json:"scripts"`
		DevDependencies map[string]string `json:"devDependencies"`
	}{Name: options.Name, Version: "0.1.0", Private: true, Type: "module", Engines: map[string]string{"node": ">=24.12.0"},
		Scripts:         map[string]string{"dev": "node --watch src/index.ts", "check": "tsc --noEmit", "build": "tsc", "start": "node dist/index.js"},
		DevDependencies: map[string]string{"typescript": "7.0.2", "@types/node": "24.19.2"},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Plan{}, err
	}
	return Plan{TemplateID: "node-typescript", Files: []File{
		{Path: "package.json", Contents: string(encoded) + "\n"},
		{Path: "tsconfig.json", Contents: `{
  "compilerOptions": {
    "target": "ES2023",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "rootDir": "src",
    "outDir": "dist",
    "strict": true,
    "noEmitOnError": true,
    "verbatimModuleSyntax": true,
    "erasableSyntaxOnly": true,
    "rewriteRelativeImportExtensions": true,
    "types": ["node"]
  },
  "include": ["src/**/*.ts"]
}
`},
		{Path: "src/index.ts", Contents: "const message: string = " + strconv.Quote("Hello from "+options.Name+"!") + ";\nconsole.log(message);\n"},
		{Path: ".gitignore", Contents: "node_modules/\ndist/\ncoverage/\n*.log\n.env\n.env.*\n!.env.example\n"},
		{Path: "README.md", Contents: "# " + options.Name + "\n\nRequires Node.js 24.12 or later and npm.\n\n## Development\n\n```sh\nnpm run dev\n```\n\nNode runs erasable TypeScript syntax directly and restarts on changes. It does not type-check or read tsconfig.json. Use explicit .ts extensions in relative imports.\n\n## Type checking and build\n\nInstall the pinned development dependencies when you approve the downloads:\n\n```sh\nnpm install --ignore-scripts\nnpm run check\nnpm run build\nnpm start\n```\n\nCommit the generated package-lock.json to pin transitive dependencies. Subsequent installs can use npm ci --ignore-scripts. Creation itself does not download dependencies or run code.\n"},
	}, InitializationCommands: []string{}, StartupName: "Develop Node.js application", StartupProgram: "node", StartupArguments: []string{"--watch", "src/index.ts"}, Preview: "Command-line output; no browser preview.", DependencySetup: &Setup{
		Program: "npm", Arguments: []string{"install", "--ignore-scripts"}, Note: "Downloads the pinned TypeScript compiler and Node type definitions for type checking/building. Not needed for native development. Not run during creation; npm will generate package-lock.json.",
	}}, nil
}
