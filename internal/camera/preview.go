package camera

import "strings"

func requireH264(tracks []string) error {
	for _, track := range tracks {
		if strings.EqualFold(track, "H264") {
			return nil
		}
	}
	return failure(422, "摄像头必须输出H.264，请在摄像头编码设置中选择H.264后重新连接")
}
