package testdb

// Constants used in tests, these are tied to the DB created by the RapidPro `mailroom_db` management command.

var AuthGroupIDs = map[string]int{
	"Alpha":            1,
	"Beta":             2,
	"Dashboard":        3,
	"Surveyors":        4,
	"Customer Support": 5,
	"Granters":         6,
	"Administrators":   7,
	"Editors":          8,
	"Viewers":          9,
	"Agents":           10,
	"Prometheus":       11,
}

var Org1 = &Org{1, "bf0514a5-9407-44c9-b0f9-3f36f9c18414"}
var Admin = &User{3, "e29fdf9f-56ab-422a-b77d-e3ec26091a25"}
var Editor = &User{4, "ea84a3d7-92e4-4a8b-b01f-db11ff83bbb6"}
var Agent = &User{5, "27e44553-ab35-482b-a4d4-6f000ec611ab"}

var TwilioChannel = &Channel{10000, "74729f45-7f29-4868-9dc4-90e491e3c7d8", "T"}
var VonageChannel = &Channel{10001, "19012bfd-3ce3-4cae-9bb9-76cf92c73d49", "NX"}
var FacebookChannel = &Channel{10002, "0f661e8b-ea9d-4bd3-9953-d368340acf91", "FBA"}
var AndroidChannel = &Channel{10003, "8e7b62ee-2e84-4601-8fef-2e44c490b43e", "A"}

var Ann = &Contact{10000, "a393abc0-283d-4c9b-a1b3-641a035c34bf", "tel:+16055741111", 10000}
var Bob = &Contact{10001, "b699a406-7e44-49be-9f01-1a82893e8a10", "tel:+16055742222", 10001}
var Cat = &Contact{10002, "cd024bcd-f473-4719-a00a-bd0bb1190135", "tel:+16055743333", 10002}
var Dan = &Contact{10003, "d709c157-4606-4d41-9df3-9e9c9b4ae2d4", "tel:+593999123456", 10003}

var Favorites = &Flow{10000, "9de3663f-c5c5-4c92-9f45-ecbc09abcc85"}
var PickANumber = &Flow{10001, "5890fe3a-f204-4661-b74d-025be4ee019c"}
var SingleMessage = &Flow{10004, "a7c11d68-f008-496f-b56d-2d5cf4cf16a5"}
var IVRFlow = &Flow{10003, "2f81d0ea-4d75-4843-9371-3f7465311cce"}
var IncomingExtraFlow = &Flow{10006, "376d3de6-7f0e-408c-80d6-b1919738bc80"}
var ParentTimeoutFlow = &Flow{10007, "81c0f323-7e06-4e0c-a960-19c20f17117c"}
var ChildTimeoutFlow = &Flow{10008, "7a7ab82c-9fff-49f3-a390-a2957fd60834"}
var BackgroundFlow = &Flow{10009, "1ff89517-5735-466b-be31-682b49521cbf"}

var CreatedOnField = &Field{10000, "606de307-a799-47fc-8802-edc9301e0e04"}
var LastSeenOnField = &Field{10001, "53499958-0a0a-48a5-bb5f-8f9f4d8af77b"}
var GenderField = &Field{10002, "3a5891e4-756e-4dc9-8e12-b7a766168824"}
var AgeField = &Field{10003, "903f51da-2717-47c7-a0d3-f2f32877013d"}
var JoinedField = &Field{10004, "d83aae24-4bbf-49d0-ab85-6bfd201eac6d"}
var WardField = &Field{10005, "de6878c1-b174-4947-9a65-8910ebe7d10f"}
var DistrictField = &Field{10006, "3ca3e36b-3d5a-42a4-b292-482282ce9a90"}
var StateField = &Field{10007, "1dddea55-9a3b-449f-9d43-57772614ff50"}

var ActiveGroup = &Group{10000, "b97f69f7-5edf-45c7-9fda-d37066eae91d"}
var BlockedGroup = &Group{10001, "14f6ea01-456b-4417-b0b8-35e942f549f1"}
var StoppedGroup = &Group{10002, "d1ee73f0-bdb5-47ce-99dd-0c95d4ebf008"}
var ArchivedGroup = &Group{10003, "9295ebab-5c2d-4eb1-86f9-7c15ed2f3219"}
var OpenTicketsGroup = &Group{10004, "361838c4-2866-495a-8990-9f3c222a7604"}
var DoctorsGroup = &Group{10005, "c153e265-f7c9-4539-9dbc-9b358714b638"}
var TestersGroup = &Group{10006, "5e9d8fab-5e7e-4f51-b533-261af5dea70d"}

var ReportingLabel = &Label{10000, "ebc4dedc-91c4-4ed4-9dd6-daa05ea82698"}
var TestingLabel = &Label{10001, "a6338cdc-7938-4437-8b05-2d5d785e3a08"}

var ReviveTemplate = &Template{10000, "9c22b594-fcab-4b29-9bcb-ce4404894a80"}
var GoodbyeTemplate = &Template{10001, "3b8dd151-1a91-411f-90cb-dd9065bb7a71"}

var DefaultTopic = &Topic{10000, "4307df2e-b00b-42b6-922b-4a1dcfc268d8"}
var SalesTopic = &Topic{10001, "9ef2ff21-064a-41f1-8560-ccc990b4f937"}
var SupportTopic = &Topic{10002, "0a8f2e00-fef6-402c-bd79-d789446ec0e0"}

var Partners = &Team{10001, "4321c30b-b596-46fa-adb4-4a46d37923f6"}
var Office = &Team{10002, "f14c1762-d38b-4072-ae63-2705332a3719"}

var Luis = &Classifier{10000, "097e026c-ae79-4740-af67-656dbedf0263"}
var Wit = &Classifier{10001, "ff2a817c-040a-4eb2-8404-7d92e8b79dd0"}
var Bothub = &Classifier{10002, "859b436d-3005-4e43-9ad5-3de5f26ede4c"}

var OpenAI = &LLM{10000, "62c2bb93-f388-4c72-a2e6-25bee7282240"}
var Anthropic = &LLM{10001, "43764a92-2545-4aa0-b005-6ade894acc96"}
var TestLLM = &LLM{10002, "e5d8900a-ef54-4d2a-8214-ff7d3e903502"}

var RemindersCampaign = &Campaign{10000, "72aa12c5-cc11-4bc7-9406-044047845c70"}
var RemindersPoint1 = &CampaignPoint{10000, "3c8eca88-a5f8-4e27-96f4-e47f19cc0de8"} // joined + 5 days => Favorites, interrupts
var RemindersPoint2 = &CampaignPoint{10001, "f2a3f8c5-e831-4df3-b046-8d8cdb90f178"} // joined + 10 minutes => "Hi @contact...", passive
var RemindersPoint3 = &CampaignPoint{10002, "552a7155-66bc-4323-aad0-8421f87a4e0c"} // joined + 1 week => Pick A Number, skips

// secondary org.. only a few things
var Org2 = &Org{2, "3ae7cdeb-fd96-46e5-abc4-a4622f349921"}
var Org2Admin = &User{7, "18afc5e7-e97b-45e5-9850-8896957fef54"}
var Org2Channel = &Channel{20000, "a89bc872-3763-4b95-91d9-31d4e56c6651", "T"}
var Org2Contact = &Contact{20000, "f6d20b72-f7d8-44dc-87f2-aae046dbff95", "tel:+250700000005", 20000}
var Org2Favorites = &Flow{20000, "f161bd16-3c60-40bd-8c92-228ce815b9cd"}
var Org2SingleMessage = &Flow{20001, "5277916d-6011-41ac-a4a4-f6ac6a4f1dd9"}
