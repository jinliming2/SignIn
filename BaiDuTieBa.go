package main

import (
    "compress/gzip"
    "crypto/md5"
    "encoding/json"
    "fmt"
    "github.com/mozillazg/request"
    "golang.org/x/text/encoding/simplifiedchinese"
    "io/ioutil"
    "net/http"
    "os"
    "regexp"
    "sort"
    "time"
)

//配置文件格式
type Config struct {
    BAIDUID string
    BDUSS   string
    STOKEN  string
}

//签到结果格式
type SignResult struct {
    ctime       int
    error       SignError
    error_code  string
    error_msg   string
    logid       int
    server_time string
    time        int
    user_info   SignUserInfo
}

//签到错误信息格式
type SignError struct {
    errmsg  string
    errno   string
    usermsg string
}

//签到结果详情格式
type SignUserInfo struct {
    cont_sign_num       string //连续签到
    cout_total_sing_num string //累计签到
    hun_sign_num        string
    is_org_name         string
    is_sign_in          string //是否签到成功
    level_name          string //等级称号
    levelup_score       string //升级需要经验
    miss_sign_num       string //漏签天数
    sign_bonus_point    string
    sign_time           string //签到时间
    total_resign_num    string
    total_sign_num      string
    user_id             string
    user_sign_rank      string //签到排名
}

//错误代码
const (
    ERROR_OPEN_CONFIG_FAIL       = iota + 1
    ERROR_LOAD_CONFIG_FAIL
    ERROR_COMPILE_REGEXP_FAIL
    ERROR_FETCH_LIKED_BAR_FAIL
    ERROR_PARSE_LIKED_BAR_FAIL
    ERROR_LOAD_LIKED_BAR_FAIL
    ERROR_FETCH_BAR_INFO_FAIL
    ERROR_PARSE_BAR_INFO_FAIL
    ERROR_SIGN_REQUEST_FAIL
    ERROR_LOAD_SIGN_RESULT_FAIL
    ERROR_PARSE_SIGN_RESULT_FAIL
)

//浏览器
var browser = request.NewRequest(new(http.Client))
//配置文件
var config = loadConfig()

//GBK编码转换器
var gbk = simplifiedchinese.GBK.NewDecoder()
//正则
var likedBar, barInfoSigned, barInfoFid, barInfoTbs *regexp.Regexp

//初始化
func init() {
    var err error
    likedBar, err = regexp.Compile(`<a href="/f\?kw=(.*?)" title=".*?">(.+?)</a>`)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Compile Regexp Error: %v\n", err)
        os.Exit(ERROR_COMPILE_REGEXP_FAIL)
    }
    barInfoSigned, err = regexp.Compile(`<td style="text-align:right;"><span[ ]>(.*?)</span></td></tr>`)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Compile Regexp Error: %v\n", err)
        os.Exit(ERROR_COMPILE_REGEXP_FAIL)
    }
    barInfoFid, err = regexp.Compile(`<input type="hidden" name="fid" value="(.+?)"/>`)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Compile Regexp Error: %v\n", err)
        os.Exit(ERROR_COMPILE_REGEXP_FAIL)
    }
    barInfoTbs, err = regexp.Compile(`<input type="hidden" name="tbs" value="(.+?)"/>`)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Compile Regexp Error: %v\n", err)
        os.Exit(ERROR_COMPILE_REGEXP_FAIL)
    }
    browser.Cookies = map[string]string{
        "BAIDU_WISE_UID": "wapp_1456586369546_813",
        "BAIDUID":        config.BAIDUID,
        "BDUSS":          config.BDUSS,
        "STOKEN":         config.STOKEN,
    }
    browser.Headers = map[string]string{
        "User-agent":      "Mozilla/5.0 (SymbianOS/9.3; Series60/3.2 NokiaE72-1/021.021; Profile/MIDP-2.1 Configuration/CLDC-1.1 ) AppleWebKit/525 (KHTML, like Gecko) Version/3.0 BrowserNG/7.1.16352",
        "Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Encoding": "gzip,deflate",
    }
}

func main() {
    //加载贴吧列表
    like_list := fetchLikeList()

    //任务队列和结果队列
    queue := make(chan []string)
    done := make(chan SignResult)
    defer close(queue)
    defer close(done)

    //等待时间
    _time := make(chan int)
    go waitTime(_time)
    <-_time
    close(_time)

    //多线程处理任务
    go func() {
        for {
            task := <-queue
            go func() {
                _, fid, tbs := getInfo(task[1])
                done <- signRequest(task[2], fid, tbs)
            }()
        }
    }()
    //导入任务队列
    for i := range like_list {
        queue <- like_list[i]
    }
    //等待结果
    for i := 0; i < len(like_list); i++ {
        result := <-done
        if result.error_code == "0" {
            if result.user_info.is_sign_in == "1" {
                //签到成功
            }
        }
    }
}

//读取加载配置文件
func loadConfig() Config {
    file, err := os.Open("./BaiDuTieBa.json")
    if err != nil {
        fmt.Fprintf(os.Stderr, "Open Config File Error: %v\n", err)
        os.Exit(ERROR_OPEN_CONFIG_FAIL)
    }
    defer file.Close()

    var config Config
    err = json.NewDecoder(file).Decode(&config)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Load Config File Error: %v\n", err)
        os.Exit(ERROR_LOAD_CONFIG_FAIL)
    }
    return config
}

//获取时间
func getTime() time.Time {
    //百度服务器的时间
    host := "https://www.baidu.com"
    req, err := request.Head(host, nil)
    if err != nil {
        fmt.Fprintf(os.Stdout, "Cannot connect to %v: %v\n", host, err)
        return time.Now()
    }
    //百度的时间采用的是RFC1123标准格式
    t, err := time.Parse(time.RFC1123, req.Header.Get("date"))
    if err != nil {
        fmt.Fprintf(os.Stdout, "Cannot Parse Time From BaiDu: %v\n", err)
        return time.Now()
    }
    //亚洲-上海时区，北京时间，东八区
    location, err := time.LoadLocation("Asia/Shanghai")
    if err != nil {
        fmt.Fprintf(os.Stdout, "Cannot Load China Time Zone(CST, Asia/Shanghai): %v\n", err)
        return t.Local()
    }
    return t.In(location)
}

//等待时间
func waitTime(done chan int) {
    // TODO: 等待0点
    done <- 0
}

//加载“喜欢的吧”列表
func fetchLikeList() [][]string {
    host := "http://tieba.baidu.com/f/like/mylike?&pn=%d"
    page := 1
    likes := [][]string{}
    for {
        res, err := browser.Get(fmt.Sprintf(host, page))
        if err != nil {
            fmt.Fprintf(os.Stderr, "Fetch Liked Bar Error: %v\n", err)
            os.Exit(ERROR_FETCH_LIKED_BAR_FAIL)
        }
        stream, err := gzip.NewReader(res.Body)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Load Loked Bar Error: %v", err)
            os.Exit(ERROR_LOAD_LIKED_BAR_FAIL)
        }
        data, err := ioutil.ReadAll(gbk.Reader(stream))
        if err != nil {
            fmt.Fprintf(os.Stderr, "Parse Liked Bar Error: %v\n", err)
            os.Exit(ERROR_PARSE_LIKED_BAR_FAIL)
        }
        like := likedBar.FindAllStringSubmatch(string(data), -1)
        if len(like) == 0 {
            break
        }
        likes = append(likes, like...)
        page++
    }
    return likes
}

//获取贴吧信息
func getInfo(name string) (string, string, string) {
    host := "http://tieba.baidu.com/mo/m?kw=%s"
    res, err := browser.Get(fmt.Sprintf(host, name))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Fetch Bar Info Error: %v\n", err)
        os.Exit(ERROR_FETCH_BAR_INFO_FAIL)
    }
    data, err := ioutil.ReadAll(res.Body)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Parse Bar Info Error: %v\n", err)
        os.Exit(ERROR_PARSE_BAR_INFO_FAIL)
    }
    _signed := barInfoSigned.FindAllStringSubmatch(string(data), -1)
    var signed, fid, tbs string
    if len(_signed) > 0 {
        signed = _signed[0][1]
    }
    _fid := barInfoFid.FindAllStringSubmatch(string(data), -1)
    if len(_fid) > 0 {
        fid = _fid[0][1]
    }
    _tbs := barInfoTbs.FindAllStringSubmatch(string(data), -1)
    if len(_tbs) > 0 {
        tbs = _tbs[0][1]
    }
    return signed, fid, tbs
}

//签到请求
func signRequest(name, fid, tbs string) SignResult {
    host := "http://c.tieba.baidu.com/c/c/forum/sign"
    browser.Data = signEncode(map[string]string{
        "BDUSS":           config.BDUSS,
        "_client_id":      "03-00-DA-59-05-00-72-96-06-00-01-00-04-00-4C-43-01-00-34-F4-02-00-BC-25-09-00-4E-36",
        "_client_type":    "4",
        "_client_version": "1.2.1.17",
        "_phone_imei":     "540b43b59d21b7a4824e1fd31b08e9a6",
        "fid":             fid,
        "kw":              name,
        "net_type":        "3",
        "tbs":             tbs,
    })
    res, err := browser.Post(host)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Sign Error: %v\n", err)
        os.Exit(ERROR_SIGN_REQUEST_FAIL)
    }
    stream, err := gzip.NewReader(res.Body)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Load Sign Info Error: %v", err)
        os.Exit(ERROR_LOAD_SIGN_RESULT_FAIL)
    }
    var result SignResult
    err = json.NewDecoder(stream).Decode(&result)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Parse Sign Info Error: %v\n", err)
        os.Exit(ERROR_PARSE_SIGN_RESULT_FAIL)
    }
    return result
}

//签到请求的数据校验
func signEncode(data map[string]string) map[string]string {
    const _sign_key = "tiebaclient!!!"
    var keys []string
    for key := range data {
        keys = append(keys, key)
    }
    sort.Strings(keys)
    s := ""
    for _, key := range keys {
        s += key + "=" + data[key]
    }
    data["sign"] = fmt.Sprintf("%X", md5.Sum([]byte(s+_sign_key)))
    return data
}
