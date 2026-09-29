export const profileGroups = {
  command_execution: "执行命令",
  ethical_blocking: "道德阻断",
  unexpected_output: "意外输出",
};

export const profileGroup = (profile) =>
  Object.hasOwn(profileGroups, profile?.category)
    ? profile.category
    : "command_execution";

export const profileGroupHints = {
  command_execution: "观察客户端是否执行指定任务并回传结果。",
  ethical_blocking: "观察客户端是否因提示内容拒绝或中止原任务。",
  unexpected_output: "观察客户端是否输出偏离原任务的内容。",
};
