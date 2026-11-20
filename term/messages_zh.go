package term

// Chinese returns the Chinese texts of the shared netops codes: failure
// codes, governance reasons, apply statuses and batch states. Tools merge
// their own codes into a copy.
func Chinese() Messages {
	return Messages{ //nolint:gosec // G101 false positive: operator texts for code names, not credentials
		// failure codes
		"cancelled": "已取消", "timeout": "超时", "auth_failed": "认证失败", "unreachable": "无法连接",
		"host_key": "主机密钥不受信任", "session_closed": "会话已断开", "session_busy": "会话忙",
		"rejected": "设备拒绝命令", "unconfirmed": "未确认", "limit_exceeded": "输出超出上限",
		"incomplete": "回显不完整", "invalid_input": "输入无效", "unsupported": "平台不支持",
		"no_credentials": "未配置凭据", "drifted": "设备配置已在规划后变化", "storage_failed": "本地保存失败",
		"protocol": "内部协议错误", "internal": "未分类错误",
		// governance reasons
		"invalid_class": "变更类别无效", "outside_window": "不在维护窗口内", "change_freeze": "处于封网期",
		"no_ticket": "缺少变更单号", "insufficient_approvals": "审批人数不足", "self_approval": "不能自行审批",
		"approval_expired": "审批已过期", "approval_for_other_plan": "审批对应的不是该计划",
		"too_many_devices": "单次变更设备数超限", "no_justification": "紧急变更须填写理由",
		"window_bypassed": "紧急变更绕过维护窗口", "freeze_bypassed": "紧急变更绕过封网期",
		// apply statuses
		"planned": "已规划", "executing": "执行中", "verified": "已验证", "satisfied": "无需变更",
		"dry_run": "演练通过", "failed": "失败", "uncertain": "结果不确定（可能已下发）",
		// batch states
		"succeeded": "成功", "skipped": "未执行",
		// cache
		"offline": "离线模式", "backoff": "失败退避中",
	}
}
