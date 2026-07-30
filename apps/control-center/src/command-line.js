function splitCommandLine(input) {
  const tokens = [];
  let current = "";
  let quote = null;
  let escaping = false;
  let tokenStarted = false;

  for (const char of String(input || "")) {
    if (escaping) {
      current += char;
      escaping = false;
      tokenStarted = true;
      continue;
    }
    if (char === "\\") {
      escaping = true;
      tokenStarted = true;
      continue;
    }
    if (quote) {
      if (char === quote) {
        quote = null;
      } else {
        current += char;
      }
      tokenStarted = true;
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      tokenStarted = true;
      continue;
    }
    if (/\s/.test(char)) {
      if (tokenStarted) {
        tokens.push(current);
        current = "";
        tokenStarted = false;
      }
      continue;
    }
    current += char;
    tokenStarted = true;
  }

  if (escaping) {
    throw new Error("Command ends with an unfinished escape.");
  }
  if (quote) {
    throw new Error("Command has an unfinished quote.");
  }
  if (tokenStarted) {
    tokens.push(current);
  }
  return tokens;
}

module.exports = {
  splitCommandLine
};
