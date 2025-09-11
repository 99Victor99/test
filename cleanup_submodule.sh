#!/bin/bash

# Git 子模块清理脚本
# 用法: ./cleanup_submodule.sh <submodule_path>

if [ $# -eq 0 ]; then
    echo "用法: $0 <submodule_path>"
    echo "例如: $0 proto/goods"
    exit 1
fi

SUBMODULE_PATH=$1

echo "清理子模块: $SUBMODULE_PATH"

# 1. 删除目标目录
echo "1. 删除目录: $SUBMODULE_PATH"
rm -rf "$SUBMODULE_PATH"

# 2. 清理 Git 缓存
echo "2. 清理 Git 缓存"
git rm --cached "$SUBMODULE_PATH" 2>/dev/null || echo "   没有缓存需要清理"

# 3. 清理未跟踪文件
echo "3. 清理未跟踪文件"
git clean -fd "$SUBMODULE_PATH" 2>/dev/null || echo "   没有未跟踪文件需要清理"

# 4. 删除 Git 模块信息（关键步骤）
echo "4. 删除 Git 模块信息"
rm -rf ".git/modules/$SUBMODULE_PATH"

# 5. 取消子模块初始化
echo "5. 取消子模块初始化"
git submodule deinit -f "$SUBMODULE_PATH" 2>/dev/null || echo "   没有子模块需要取消初始化"

# 6. 清理 Git 配置
echo "6. 清理 Git 配置"
git config --remove-section "submodule.$SUBMODULE_PATH" 2>/dev/null || echo "   没有配置需要清理"

echo "清理完成！现在可以重新添加子模块了。"

