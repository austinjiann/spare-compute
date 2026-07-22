import Testing
@testable import ComputeHopApp

@Test
func commandInputPreservesLiteralArgumentsWithoutShellExpansion() throws {
    let arguments = try CommandInput.parse("cargo build --message 'hello world' \"$HOME\" empty=\\ value")
    #expect(arguments == ["cargo", "build", "--message", "hello world", "$HOME", "empty= value"])
}

@Test
func commandInputSupportsEmptyQuotedArgument() throws {
    #expect(try CommandInput.parse("printf ''") == ["printf", ""])
}

@Test(arguments: [
    ("", CommandInputError.empty),
    ("echo \\", CommandInputError.trailingEscape),
    ("echo 'unfinished", CommandInputError.unclosedSingleQuote),
    ("echo \"unfinished", CommandInputError.unclosedDoubleQuote),
])
func commandInputRejectsIncompleteInput(input: String, expected: CommandInputError) {
    #expect(throws: expected) {
        try CommandInput.parse(input)
    }
}
