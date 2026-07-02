import type { Comment } from "@/lib/types";
import { UserAvatar } from "@/components/admin/UserAvatar";

interface CommentAvatarProps {
  c: Comment;
  size?: number;
}

export function CommentAvatar({ c, size = 20 }: CommentAvatarProps) {
  return (
    <UserAvatar
      user={{
        nickname: c.author_nickname || "",
        username: c.author_name || "匿名",
        avatar: c.author_avatar || "",
      }}
      size={size}
    />
  );
}
