const { inspectProject } = require("./planner");
const { planTaskWithOpenAI } = require("./openai-planner");

async function planControlCenterTask(request: any = {}, options: any = {}) {
  const task = String(request.task || "").trim();
  if (!task) {
    return { ok: false, error: "Enter what you want to do." };
  }

  const projectRoot = String(request.projectRoot || "").trim();
  const profile = await inspectProject(projectRoot);
  return planTaskWithOpenAI(
    {
      ...request,
      task,
      projectRoot,
      profile
    },
    options
  );
}

module.exports = {
  planControlCenterTask
};
