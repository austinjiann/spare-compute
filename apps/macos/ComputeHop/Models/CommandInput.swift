import Foundation

enum CommandInputError: LocalizedError, Equatable {
    case empty
    case trailingEscape
    case unclosedSingleQuote
    case unclosedDoubleQuote

    var errorDescription: String? {
        switch self {
        case .empty:
            return "Enter a command to run."
        case .trailingEscape:
            return "The command ends with an unfinished escape."
        case .unclosedSingleQuote:
            return "The command has an unclosed single quote."
        case .unclosedDoubleQuote:
            return "The command has an unclosed double quote."
        }
    }
}

enum CommandInput {
    private enum Quote {
        case single
        case double
    }

    /// Splits a human-entered command into an executable and literal arguments.
    /// Quotes and backslashes only group text; no shell expansion is performed.
    static func parse(_ input: String) throws -> [String] {
        var arguments: [String] = []
        var current = ""
        var quote: Quote?
        var escaping = false
        var tokenStarted = false

        for character in input {
            if escaping {
                current.append(character)
                escaping = false
                tokenStarted = true
                continue
            }

            switch quote {
            case .single:
                if character == "'" {
                    quote = nil
                } else {
                    current.append(character)
                }
                tokenStarted = true
            case .double:
                if character == "\"" {
                    quote = nil
                } else if character == "\\" {
                    escaping = true
                } else {
                    current.append(character)
                }
                tokenStarted = true
            case nil:
                if character.isWhitespace {
                    if tokenStarted {
                        arguments.append(current)
                        current = ""
                        tokenStarted = false
                    }
                } else if character == "'" {
                    quote = .single
                    tokenStarted = true
                } else if character == "\"" {
                    quote = .double
                    tokenStarted = true
                } else if character == "\\" {
                    escaping = true
                    tokenStarted = true
                } else {
                    current.append(character)
                    tokenStarted = true
                }
            }
        }

        if escaping {
            throw CommandInputError.trailingEscape
        }
        switch quote {
        case .single:
            throw CommandInputError.unclosedSingleQuote
        case .double:
            throw CommandInputError.unclosedDoubleQuote
        case nil:
            break
        }
        if tokenStarted {
            arguments.append(current)
        }
        guard !arguments.isEmpty else {
            throw CommandInputError.empty
        }
        return arguments
    }
}
