const { planTask, suggestTasks } = require("./planner");
const { openAIPlannerConfig, planTaskWithOpenAI } = require("./openai-planner");

async function planControlCenterTask(request = {}, options = {}) {
  const local = await planTask(request);
  if (local.ok || !shouldTryAIPlanner(local, request, options)) {
    return local;
  }

  const ai = await planTaskWithOpenAI(
    {
      ...request,
      profile: local.profile
    },
    options
  );
  if (ai.ok) {
    return ai;
  }

  return {
    ...local,
    aiPlanner: {
      attempted: true,
      error: ai.error || "AI planner could not make a safe command."
    }
  };
}

async function suggestControlCenterTasks(request = {}) {
  return suggestTasks(request);
}

function shouldTryAIPlanner(local, request = {}, options = {}) {
  const error = String(local?.error || "");
  if (/Choose a project first/i.test(error)) {
    return false;
  }
  const config = options.config || openAIPlannerConfig(options.env || process.env);
  return Boolean(config.configured && String(request.task || "").trim());
}

module.exports = {
  planControlCenterTask,
  shouldTryAIPlanner,
  suggestControlCenterTasks
};
